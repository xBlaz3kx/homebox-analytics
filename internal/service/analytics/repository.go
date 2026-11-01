package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Values maps item ID -> bucket key (bucketLayout, local wall time) -> value.
type Values map[string]map[string]float64

type Repository interface {
	// Aggregate returns the metric per item per bucket for events in [from, to).
	Aggregate(ctx context.Context, itemIDs []string, metric Metric, interval Interval, tz string, from, to time.Time) (Values, error)
	// QuantityBefore returns each item's last recorded quantity before t (items without events are omitted).
	QuantityBefore(ctx context.Context, itemIDs []string, t time.Time) (map[string]float64, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// metricExpr is the per-bucket aggregate for each metric. Consumption includes negative
// reconciliation deltas (stock found missing during a count).
var metricExpr = map[Metric]string{
	MetricPurchased: `COALESCE(SUM(delta) FILTER (WHERE type = 'purchase'), 0)`,
	MetricConsumed:  `COALESCE(-SUM(delta) FILTER (WHERE type = 'consume' OR (type = 'reconcile' AND delta < 0)), 0)`,
	MetricSpend:     `COALESCE(SUM(delta * unit_price) FILTER (WHERE type = 'purchase' AND unit_price IS NOT NULL), 0)`,
	MetricEvents:    `COUNT(*)`,
	MetricQuantity:  `(array_agg(quantity_after ORDER BY occurred_at DESC, created_at DESC))[1]`,
}

func (r *PostgresRepository) Aggregate(ctx context.Context, itemIDs []string, metric Metric, interval Interval, tz string, from, to time.Time) (Values, error) {
	expr, ok := metricExpr[metric]
	if !ok {
		return nil, fmt.Errorf("unknown metric %q", metric)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT item_id,
		       to_char(date_trunc($2, occurred_at AT TIME ZONE $3), 'YYYY-MM-DD"T"HH24:MI:SS') AS bucket,
		       (`+expr+`)::float8
		FROM events
		WHERE item_id = ANY($1) AND occurred_at >= $4 AND occurred_at < $5
		GROUP BY 1, 2`, itemIDs, string(interval), tz, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := Values{}
	for rows.Next() {
		var (
			itemID, bucket string
			v              float64
		)
		if err := rows.Scan(&itemID, &bucket, &v); err != nil {
			return nil, err
		}
		if values[itemID] == nil {
			values[itemID] = map[string]float64{}
		}
		values[itemID][bucket] = v
	}
	return values, rows.Err()
}

func (r *PostgresRepository) QuantityBefore(ctx context.Context, itemIDs []string, t time.Time) (map[string]float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (item_id) item_id, quantity_after::float8
		FROM events
		WHERE item_id = ANY($1) AND occurred_at < $2
		ORDER BY item_id, occurred_at DESC, created_at DESC`, itemIDs, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var (
			id string
			q  float64
		)
		if err := rows.Scan(&id, &q); err != nil {
			return nil, err
		}
		out[id] = q
	}
	return out, rows.Err()
}
