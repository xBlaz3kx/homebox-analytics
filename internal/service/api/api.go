// Package api exposes the service over HTTP using gin.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/scan"
)

type Catalog interface {
	Resolve(ctx context.Context, q catalog.LookupQuery) (catalog.LookupResult, error)
	LinkCode(ctx context.Context, code, vendor, itemID string, source catalog.CodeSource) (*catalog.CodeLink, error)
	ListCodes(ctx context.Context, itemID string) ([]catalog.CodeLink, error)
	DeleteCode(ctx context.Context, id string) error
	Sync(ctx context.Context) (catalog.SyncStats, error)
}

type Ledger interface {
	RecordEvent(ctx context.Context, in ledger.EventInput) (*ledger.Event, error)
	Reconcile(ctx context.Context, in ledger.ReconcileInput) (*ledger.Reconciliation, error)
	ReconcileBatch(ctx context.Context, in []ledger.ReconcileInput) []ledger.ReconcileResult
	ListEvents(ctx context.Context, f ledger.EventFilter) ([]ledger.Event, error)
	ListReconciliations(ctx context.Context, itemID string, limit int) ([]ledger.Reconciliation, error)
}

type Analytics interface {
	Series(ctx context.Context, q analytics.Query) (*analytics.Chart, error)
}

// Check is a named readiness probe.
type Check struct {
	Name  string
	Probe func(ctx context.Context) error
}

type Handler struct {
	catalog   Catalog
	ledger    Ledger
	analytics Analytics
	scanner   *scan.Service
	checks    []Check
	logger    *zap.Logger
}

func NewHandler(c Catalog, l Ledger, a Analytics, checks []Check, logger *zap.Logger) http.Handler {
	h := &Handler{catalog: c, ledger: l, analytics: a, scanner: scan.NewService(c, l), checks: checks, logger: logger}

	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/readyz", h.ready)

	v1 := router.Group("/api/v1", otelgin.Middleware("homebox-analytics"))

	items := v1.Group("/items")
	{
		items.GET("/lookup", h.lookup)

		item := items.Group("/:id")
		item.GET("/codes", h.listCodes)
		item.GET("/events", h.listEvents)
		item.POST("/events", h.recordEvent)
		item.POST("/reconcile", h.reconcile)
	}

	codes := v1.Group("/codes")
	{
		codes.POST("", h.linkCode)
		codes.DELETE("/:id", h.deleteCode)
	}

	stock := v1.Group("")
	{
		stock.POST("/scan", h.scan)
		stock.POST("/reconcile", h.reconcileBatch)
		stock.GET("/reconciliations", h.listReconciliations)
	}

	charts := v1.Group("/charts")
	{
		charts.GET("", h.chartQuery)
		charts.POST("", h.chartBody)
	}

	v1.POST("/sync", h.sync)

	return router
}

func (h *Handler) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	status := map[string]string{}
	code := http.StatusOK
	for _, check := range h.checks {
		if err := check.Probe(ctx); err != nil {
			status[check.Name] = err.Error()
			code = http.StatusServiceUnavailable
			continue
		}
		status[check.Name] = "ok"
	}
	c.JSON(code, status)
}

type errorBody struct {
	Error string `json:"error"`
	Data  any    `json:"data,omitempty"`
}

func decode(c *gin.Context, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &badRequest{"invalid JSON body: " + err.Error()}
	}
	return nil
}

type badRequest struct{ msg string }

func (e *badRequest) Error() string { return e.msg }

// writeError maps domain errors to HTTP statuses.
func (h *Handler) writeError(c *gin.Context, err error) {
	var (
		status   = http.StatusInternalServerError
		bad      *badRequest
		ledgerVE *ledger.ValidationError
		chartVE  *analytics.ValidationError
	)
	switch {
	case errors.As(err, &bad), errors.As(err, &ledgerVE), errors.As(err, &chartVE), errors.Is(err, catalog.ErrEmptyQuery):
		status = http.StatusBadRequest
	case errors.Is(err, catalog.ErrNotFound), errors.Is(err, ledger.ErrNotFound), errors.Is(err, homebox.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ledger.ErrInsufficientQuantity):
		status = http.StatusConflict
	}

	msg := err.Error()
	if status == http.StatusInternalServerError {
		h.logger.Error("Request failed", zap.String("path", c.FullPath()), zap.Error(err))
		msg = "internal error"
	}
	c.JSON(status, errorBody{Error: msg})
}
