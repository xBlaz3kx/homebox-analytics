package ledger

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx is a unit of work holding a per-item lock until commit or rollback.
type Tx interface {
	InsertEvent(ctx context.Context, e Event) (*Event, error)
	InsertReconciliation(ctx context.Context, r Reconciliation) (*Reconciliation, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type Repository interface {
	// BeginItem starts a transaction and takes an exclusive lock for itemID, serializing
	// concurrent quantity changes to the same item.
	BeginItem(ctx context.Context, itemID string) (Tx, error)
	ListEvents(ctx context.Context, f EventFilter) ([]Event, error)
	ListReconciliations(ctx context.Context, itemID string, limit int) ([]Reconciliation, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func lockKey(itemID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("ledger:" + itemID))
	return int64(h.Sum64())
}

func (r *PostgresRepository) BeginItem(ctx context.Context, itemID string) (Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey(itemID)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &pgTx{tx: tx}, nil
}

type pgTx struct {
	tx pgx.Tx
}

const eventColumns = `id::text, item_id, type, delta::float8, quantity_after::float8, unit_price::float8, currency, vendor, code_used, note, occurred_at, created_at`

func scanEvent(row pgx.Row) (*Event, error) {
	var e Event
	err := row.Scan(&e.ID, &e.ItemID, &e.Type, &e.Delta, &e.QuantityAfter, &e.UnitPrice, &e.Currency, &e.Vendor, &e.CodeUsed, &e.Note, &e.OccurredAt, &e.CreatedAt)
	return &e, err
}

func (t *pgTx) InsertEvent(ctx context.Context, e Event) (*Event, error) {
	return scanEvent(t.tx.QueryRow(ctx, `
		INSERT INTO events (item_id, type, delta, quantity_after, unit_price, currency, vendor, code_used, note, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+eventColumns,
		e.ItemID, e.Type, e.Delta, e.QuantityAfter, e.UnitPrice, e.Currency, e.Vendor, e.CodeUsed, e.Note, e.OccurredAt))
}

const reconciliationColumns = `id::text, item_id, homebox_qty::float8, counted_qty::float8, delta::float8, applied, note, created_at`

func scanReconciliation(row pgx.Row) (*Reconciliation, error) {
	var r Reconciliation
	err := row.Scan(&r.ID, &r.ItemID, &r.HomeboxQty, &r.CountedQty, &r.Delta, &r.Applied, &r.Note, &r.CreatedAt)
	return &r, err
}

func (t *pgTx) InsertReconciliation(ctx context.Context, r Reconciliation) (*Reconciliation, error) {
	return scanReconciliation(t.tx.QueryRow(ctx, `
		INSERT INTO reconciliations (item_id, homebox_qty, counted_qty, delta, applied, note)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+reconciliationColumns,
		r.ItemID, r.HomeboxQty, r.CountedQty, r.Delta, r.Applied, r.Note))
}

func (t *pgTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

func (r *PostgresRepository) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+eventColumns+` FROM events
		WHERE item_id = $1
		  AND ($2::timestamptz IS NULL OR occurred_at >= $2)
		  AND ($3::timestamptz IS NULL OR occurred_at < $3)
		ORDER BY occurred_at DESC, created_at DESC
		LIMIT $4`, f.ItemID, nullTime(f.From), nullTime(f.To), f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, rows.Err()
}

func (r *PostgresRepository) ListReconciliations(ctx context.Context, itemID string, limit int) ([]Reconciliation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+reconciliationColumns+` FROM reconciliations
		WHERE ($1 = '' OR item_id = $1)
		ORDER BY created_at DESC
		LIMIT $2`, itemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recs := []Reconciliation{}
	for rows.Next() {
		rec, err := scanReconciliation(rows)
		if err != nil {
			return nil, err
		}
		recs = append(recs, *rec)
	}
	return recs, rows.Err()
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
