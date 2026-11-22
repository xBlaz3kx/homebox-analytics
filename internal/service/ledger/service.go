package ledger

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/observability"
)

var tracer = otel.Tracer("github.com/xBlaz3kx/homebox-analytics/internal/service/ledger")

// Inventory is the source of truth for quantities (Homebox).
type Inventory interface {
	GetEntity(ctx context.Context, id string) (*homebox.Entity, error)
	SetQuantity(ctx context.Context, id string, quantity float64) (*homebox.Entity, error)
}

type Service struct {
	repo      Repository
	inventory Inventory
	logger    *zap.Logger
	now       func() time.Time

	events         metric.Int64Counter
	reconcileDelta metric.Float64Histogram
	homeboxErrors  metric.Int64Counter
}

func NewService(repo Repository, inventory Inventory, logger *zap.Logger) (*Service, error) {
	meter := otel.Meter("homebox-analytics")
	s := &Service{repo: repo, inventory: inventory, logger: logger, now: time.Now}

	var err error
	if s.events, err = meter.Int64Counter("hba.ledger.events", metric.WithDescription("Recorded ledger events by type")); err != nil {
		return nil, err
	}
	if s.reconcileDelta, err = meter.Float64Histogram("hba.reconcile.delta", metric.WithDescription("Counted minus Homebox quantity")); err != nil {
		return nil, err
	}
	if s.homeboxErrors, err = meter.Int64Counter("hba.homebox.errors", metric.WithDescription("Failed Homebox calls by operation")); err != nil {
		return nil, err
	}
	return s, nil
}

// RecordEvent applies a purchase/consume/adjust to the Homebox quantity and logs it.
func (s *Service) RecordEvent(ctx context.Context, in EventInput) (_ *Event, err error) {
	ctx, span := tracer.Start(ctx, "ledger.RecordEvent", trace.WithAttributes(
		attribute.String("item_id", in.ItemID), attribute.String("type", string(in.Type))))
	defer observability.End(span, &err)

	delta, err := in.Delta()
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Float64("delta", delta))
	if in.OccurredAt.IsZero() {
		in.OccurredAt = s.now()
	}

	var event *Event
	err = s.withItem(ctx, in.ItemID, func(tx Tx, current float64) (float64, error) {
		next := current + delta
		if next < 0 && in.Type == EventConsume && !in.AllowNegative {
			return 0, fmt.Errorf("%w: have %g, consuming %g", ErrInsufficientQuantity, current, in.Quantity)
		}

		event, err = tx.InsertEvent(ctx, Event{
			ItemID: in.ItemID, Type: in.Type, Delta: delta, QuantityAfter: next,
			UnitPrice: in.UnitPrice, Currency: in.Currency, Vendor: in.Vendor,
			CodeUsed: in.CodeUsed, Note: in.Note, OccurredAt: in.OccurredAt,
		})
		return next, err
	})
	if err != nil {
		return nil, err
	}

	s.events.Add(ctx, 1, metric.WithAttributes(attribute.String("type", string(in.Type))))
	return event, nil
}

// Reconcile compares a counted quantity with Homebox. With Apply, the counted quantity is pushed
// to Homebox and the difference is recorded as a reconcile event.
func (s *Service) Reconcile(ctx context.Context, in ReconcileInput) (_ *Reconciliation, err error) {
	ctx, span := tracer.Start(ctx, "ledger.Reconcile", trace.WithAttributes(
		attribute.String("item_id", in.ItemID), attribute.Bool("apply", in.Apply)))
	defer observability.End(span, &err)

	if in.ItemID == "" {
		return nil, &ValidationError{"item_id is required"}
	}
	if in.Counted < 0 {
		return nil, &ValidationError{"counted quantity must not be negative"}
	}

	var rec *Reconciliation
	err = s.withItem(ctx, in.ItemID, func(tx Tx, current float64) (float64, error) {
		delta := in.Counted - current
		applied := in.Apply && delta != 0

		rec, err = tx.InsertReconciliation(ctx, Reconciliation{
			ItemID: in.ItemID, HomeboxQty: current, CountedQty: in.Counted,
			Delta: delta, Applied: applied, Note: in.Note,
		})
		if err != nil || !applied {
			return current, err
		}

		rec.Event, err = tx.InsertEvent(ctx, Event{
			ItemID: in.ItemID, Type: EventReconcile, Delta: delta, QuantityAfter: in.Counted,
			Note: in.Note, OccurredAt: s.now(),
		})
		return in.Counted, err
	})
	if err != nil {
		return nil, err
	}

	span.SetAttributes(attribute.Float64("delta", rec.Delta), attribute.Bool("applied", rec.Applied))
	s.reconcileDelta.Record(ctx, rec.Delta, metric.WithAttributes(attribute.Bool("applied", rec.Applied)))
	if rec.Event != nil {
		s.events.Add(ctx, 1, metric.WithAttributes(attribute.String("type", string(EventReconcile))))
	}
	return rec, nil
}

// ReconcileBatch reconciles each input independently (stock-take); failures don't stop the batch.
func (s *Service) ReconcileBatch(ctx context.Context, inputs []ReconcileInput) []ReconcileResult {
	ctx, span := tracer.Start(ctx, "ledger.ReconcileBatch", trace.WithAttributes(attribute.Int("items", len(inputs))))
	defer span.End()

	results := make([]ReconcileResult, len(inputs))
	failed := 0
	for i, in := range inputs {
		results[i].ItemID = in.ItemID
		rec, err := s.Reconcile(ctx, in)
		if err != nil {
			results[i].Error = s.itemError(in.ItemID, err)
			failed++
			continue
		}
		results[i].Reconciliation = rec
	}
	// Per-item failures are reported in the results, so the batch span itself stays OK.
	span.SetAttributes(attribute.Int("items.failed", failed))
	return results
}

// itemError is the per-item message of a failed batch entry. Caller errors (invalid input, unknown
// item, insufficient stock) are reported as-is; anything else is logged and reported generically so
// upstream (Homebox, database) details don't reach clients.
func (s *Service) itemError(itemID string, err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInsufficientQuantity) {
		return err.Error()
	}
	s.logger.Error("Reconcile failed", zap.String("item_id", itemID), zap.Error(err))
	return "internal error"
}

func (s *Service) ListEvents(ctx context.Context, f EventFilter) (_ []Event, err error) {
	ctx, span := tracer.Start(ctx, "ledger.ListEvents", trace.WithAttributes(attribute.String("item_id", f.ItemID)))
	defer observability.End(span, &err)

	events, err := s.repo.ListEvents(ctx, f)
	span.SetAttributes(attribute.Int("events", len(events)))
	return events, err
}

func (s *Service) ListReconciliations(ctx context.Context, itemID string, limit int) (_ []Reconciliation, err error) {
	ctx, span := tracer.Start(ctx, "ledger.ListReconciliations", trace.WithAttributes(attribute.String("item_id", itemID)))
	defer observability.End(span, &err)

	recs, err := s.repo.ListReconciliations(ctx, itemID, limit)
	span.SetAttributes(attribute.Int("reconciliations", len(recs)))
	return recs, err
}

// withItem locks the item, reads its live Homebox quantity, runs fn inside a transaction and pushes
// the quantity fn returns to Homebox before committing. If the commit fails after Homebox was
// updated, the Homebox quantity is restored.
func (s *Service) withItem(ctx context.Context, itemID string, fn func(tx Tx, current float64) (float64, error)) (err error) {
	tx, err := s.repo.BeginItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	entity, err := s.inventory.GetEntity(ctx, itemID)
	if err != nil {
		// Homebox answers 400 for IDs it can't parse; to the caller that is an unknown item.
		var apiErr *homebox.APIError
		if errors.Is(err, homebox.ErrNotFound) || (errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest) {
			return fmt.Errorf("item %s: %w", itemID, ErrNotFound)
		}
		s.homeboxErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("operation", "get_entity")))
		return fmt.Errorf("read homebox quantity: %w", err)
	}
	current := entity.Quantity

	next, err := fn(tx, current)
	if err != nil {
		return err
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.Float64("quantity.before", current), attribute.Float64("quantity.after", next))

	if next != current {
		if _, err = s.inventory.SetQuantity(ctx, itemID, next); err != nil {
			s.homeboxErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("operation", "set_quantity")))
			return fmt.Errorf("update homebox quantity: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		if next != current {
			span.AddEvent("homebox.quantity_restore", trace.WithAttributes(attribute.Float64("quantity", current)))
			if _, rbErr := s.inventory.SetQuantity(context.WithoutCancel(ctx), itemID, current); rbErr != nil {
				s.logger.Error("Failed to restore Homebox quantity after commit failure",
					zap.String("item_id", itemID), zap.Float64("quantity", current), zap.Error(rbErr))
			}
		}
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
