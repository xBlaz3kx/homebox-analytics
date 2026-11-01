package catalog

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
)

const indexBatchSize = 64

// Sync mirrors Homebox items into the search index. Items whose updatedAt is unchanged are
// skipped; changed items are re-embedded only when their content hash differs.
func (s *Service) Sync(ctx context.Context) (stats SyncStats, err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	ctx, span := tracer.Start(ctx, "catalog.Sync")
	start := time.Now()
	defer func() {
		stats.Duration = time.Since(start)
		span.SetAttributes(
			attribute.Int("items.seen", stats.Seen),
			attribute.Int("items.upserted", stats.Upserted),
			attribute.Int("items.deleted", stats.Deleted),
		)
		s.metrics.syncDuration.Record(ctx, stats.Duration.Seconds())
		s.metrics.syncItems.Add(ctx, int64(stats.Unchanged+stats.Touched), metric.WithAttributes(attribute.String("result", "unchanged")))
		s.metrics.syncItems.Add(ctx, int64(stats.Upserted), metric.WithAttributes(attribute.String("result", "upserted")))
		s.metrics.syncItems.Add(ctx, int64(stats.Deleted), metric.WithAttributes(attribute.String("result", "deleted")))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			s.metrics.lastSync.Store(time.Now().Unix())
		}
		span.End()
	}()

	summaries, err := s.homebox.ListAllEntities(ctx, nil)
	if err != nil {
		return stats, fmt.Errorf("list homebox items: %w", err)
	}

	existing, err := s.points.Scroll(ctx, nil)
	if err != nil {
		return stats, fmt.Errorf("scroll index: %w", err)
	}
	indexed := make(map[string]map[string]any, len(existing))
	for _, h := range existing {
		indexed[h.ID] = h.Payload
	}

	seen := make(map[string]struct{}, len(summaries))
	var batch []search.Document
	for _, sum := range summaries {
		if sum.Archived {
			continue
		}
		stats.Seen++
		seen[sum.ID] = struct{}{}

		payload, ok := indexed[sum.ID]
		updatedAt := sum.UpdatedAt.UTC().Format(time.RFC3339Nano)
		if ok && payload[fieldUpdatedAt] == updatedAt && payload[fieldModel] == s.cfg.EmbeddingModel {
			stats.Unchanged++
			continue
		}

		entity, err := s.homebox.GetEntity(ctx, sum.ID)
		if err != nil {
			return stats, fmt.Errorf("get homebox item %s: %w", sum.ID, err)
		}
		doc, err := buildDocument(entity, s.cfg.CodeFields, s.cfg.EmbeddingModel)
		if err != nil {
			return stats, fmt.Errorf("build document for %s: %w", sum.ID, err)
		}

		// Quantity changes bump updatedAt without changing searchable content.
		if ok && payload[fieldContentHash] == doc.Payload[fieldContentHash] {
			if err := s.points.SetPayload(ctx, sum.ID, map[string]any{fieldUpdatedAt: doc.Payload[fieldUpdatedAt]}); err != nil {
				return stats, fmt.Errorf("touch %s: %w", sum.ID, err)
			}
			stats.Touched++
			continue
		}

		batch = append(batch, doc)
		if len(batch) >= indexBatchSize {
			if err := s.searcher.Index(ctx, batch); err != nil {
				return stats, fmt.Errorf("index items: %w", err)
			}
			stats.Upserted += len(batch)
			batch = batch[:0]
		}
	}
	if err := s.searcher.Index(ctx, batch); err != nil {
		return stats, fmt.Errorf("index items: %w", err)
	}
	stats.Upserted += len(batch)

	var stale []string
	for id := range indexed {
		if _, ok := seen[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := s.points.Delete(ctx, stale); err != nil {
		return stats, fmt.Errorf("delete stale items: %w", err)
	}
	stats.Deleted = len(stale)

	return stats, nil
}

// RunSyncLoop syncs immediately and then every interval until ctx is cancelled.
func (s *Service) RunSyncLoop(ctx context.Context, interval time.Duration, logger *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		stats, err := s.Sync(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Error("Catalog sync failed", zap.Error(err))
		} else if err == nil {
			logger.Info("Catalog sync finished",
				zap.Int("seen", stats.Seen), zap.Int("upserted", stats.Upserted),
				zap.Int("deleted", stats.Deleted), zap.Duration("duration", stats.Duration))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
