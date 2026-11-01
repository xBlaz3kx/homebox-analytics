package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/database"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/embeddings"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/observability"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/qdrant"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/api"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

// run wires dependencies, starts the sync loop and HTTP server, and blocks until ctx is cancelled.
func run(ctx context.Context, cfg *Config, version string, baseLogger *zap.Logger) error {
	logger, shutdownTelemetry, err := observability.Setup(ctx, observability.Config{
		Enabled:     cfg.Observability.Enabled,
		ServiceName: cfg.Observability.ServiceName,
		Version:     version,
		Environment: cfg.Observability.Environment,
		Endpoint:    cfg.Observability.Endpoint,
		Protocol:    cfg.Observability.Protocol,
		Insecure:    cfg.Observability.Insecure,
		SampleRatio: cfg.Observability.SampleRatio,
	}, baseLogger)
	if err != nil {
		return fmt.Errorf("setup observability: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := shutdownTelemetry(ctx); err != nil {
			baseLogger.Warn("Telemetry shutdown failed", zap.Error(err))
		}
	}()

	pool, err := database.Connect(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}

	hb, err := homebox.New(homebox.Config{
		URL:      cfg.Homebox.URL,
		APIKey:   cfg.Homebox.APIKey,
		Username: cfg.Homebox.Username,
		Password: cfg.Homebox.Password,
		Timeout:  cfg.Homebox.Timeout,
	})
	if err != nil {
		return err
	}

	embedder, err := embeddings.New(embeddings.Config{
		BaseURL:    cfg.Embeddings.BaseURL,
		APIKey:     cfg.Embeddings.APIKey,
		Model:      cfg.Embeddings.Model,
		Dimensions: cfg.Embeddings.Dimensions,
		BatchSize:  cfg.Embeddings.BatchSize,
	})
	if err != nil {
		return err
	}

	dimCtx, cancel := context.WithTimeout(ctx, cfg.Embeddings.Timeout)
	dim, err := embedder.Dimension(dimCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("probe embedding dimension: %w", err)
	}

	index, err := qdrant.New(qdrant.Config{
		Host:       cfg.Qdrant.Host,
		Port:       cfg.Qdrant.Port,
		APIKey:     cfg.Qdrant.APIKey,
		UseTLS:     cfg.Qdrant.UseTLS,
		Collection: cfg.Qdrant.Collection,
	})
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()

	if err := index.EnsureCollection(ctx, dim, catalog.KeywordFields); err != nil {
		if errors.Is(err, qdrant.ErrDimensionMismatch) {
			return fmt.Errorf("%w (delete the %q collection to reindex with model %q)", err, cfg.Qdrant.Collection, cfg.Embeddings.Model)
		}
		return err
	}

	catalogSvc, err := catalog.NewService(catalog.Config{
		CodeFields:     cfg.Homebox.CodeFields,
		EmbeddingModel: cfg.Embeddings.Model,
		MinSimilarity:  cfg.Matching.MinSimilarity,
		MinMargin:      cfg.Matching.MinMargin,
	}, hb, catalog.NewPostgresCodeRepository(pool), search.NewHybridSearch(embedder, search.NewEncoder(), index), index)
	if err != nil {
		return err
	}

	ledgerSvc, err := ledger.NewService(ledger.NewPostgresRepository(pool), hb, logger)
	if err != nil {
		return err
	}

	analyticsSvc := analytics.NewService(analytics.NewPostgresRepository(pool), hb)

	checks := []api.Check{
		{Name: "postgres", Probe: pool.Ping},
		{Name: "qdrant", Probe: index.Ping},
		{Name: "homebox", Probe: hb.Ping},
		{Name: "embeddings", Probe: embedder.Ping},
	}

	gin.SetMode(gin.ReleaseMode)
	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           api.NewHandler(catalogSvc, ledgerSvc, analyticsSvc, checks, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go catalogSvc.RunSyncLoop(ctx, cfg.Sync.Interval, logger)

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", zap.String("address", cfg.HTTP.Address))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("Shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancelShutdown()
	return server.Shutdown(shutdownCtx)
}
