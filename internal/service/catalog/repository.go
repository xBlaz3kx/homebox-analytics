package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/barcode"
)

type CodeRepository interface {
	// Find returns the link for code, preferring a vendor-specific link over a global one.
	Find(ctx context.Context, code barcode.Code, vendor string) (*CodeLink, error)
	Upsert(ctx context.Context, link CodeLink) (*CodeLink, error)
	ListByItem(ctx context.Context, itemID string) ([]CodeLink, error)
	Delete(ctx context.Context, id string) error
}

type PostgresCodeRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresCodeRepository(pool *pgxpool.Pool) *PostgresCodeRepository {
	return &PostgresCodeRepository{pool: pool}
}

const codeColumns = `id::text, code, code_type, item_id, source, vendor, created_at`

func scanLink(row pgx.Row) (*CodeLink, error) {
	var l CodeLink
	err := row.Scan(&l.ID, &l.Code, &l.CodeType, &l.ItemID, &l.Source, &l.Vendor, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

func (r *PostgresCodeRepository) Find(ctx context.Context, code barcode.Code, vendor string) (*CodeLink, error) {
	return scanLink(r.pool.QueryRow(ctx, `
		SELECT `+codeColumns+` FROM item_codes
		WHERE code_type = $1 AND code = $2 AND vendor IN ($3, '')
		ORDER BY (vendor = $3) DESC
		LIMIT 1`, code.Type, code.Value, vendor))
}

func (r *PostgresCodeRepository) Upsert(ctx context.Context, l CodeLink) (*CodeLink, error) {
	return scanLink(r.pool.QueryRow(ctx, `
		INSERT INTO item_codes (code, code_type, item_id, source, vendor)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (code_type, code, vendor) DO UPDATE SET item_id = EXCLUDED.item_id, source = EXCLUDED.source
		RETURNING `+codeColumns, l.Code, l.CodeType, l.ItemID, l.Source, l.Vendor))
}

func (r *PostgresCodeRepository) ListByItem(ctx context.Context, itemID string) ([]CodeLink, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+codeColumns+` FROM item_codes WHERE item_id = $1 ORDER BY created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []CodeLink{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, *l)
	}
	return links, rows.Err()
}

func (r *PostgresCodeRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM item_codes WHERE id::text = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
