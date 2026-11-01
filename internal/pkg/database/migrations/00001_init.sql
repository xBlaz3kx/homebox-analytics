-- +goose Up
CREATE TABLE item_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code       TEXT        NOT NULL,
    code_type  TEXT        NOT NULL,
    item_id    TEXT        NOT NULL,
    source     TEXT        NOT NULL CHECK (source IN ('learned', 'manual')),
    vendor     TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (code_type, code, vendor)
);
CREATE INDEX item_codes_item_id_idx ON item_codes (item_id);

CREATE TABLE events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id        TEXT        NOT NULL,
    type           TEXT        NOT NULL CHECK (type IN ('purchase', 'consume', 'adjust', 'reconcile')),
    delta          NUMERIC     NOT NULL,
    quantity_after NUMERIC     NOT NULL,
    unit_price     NUMERIC,
    currency       TEXT        NOT NULL DEFAULT '',
    vendor         TEXT        NOT NULL DEFAULT '',
    code_used      TEXT        NOT NULL DEFAULT '',
    note           TEXT        NOT NULL DEFAULT '',
    occurred_at    TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_item_occurred_idx ON events (item_id, occurred_at);

CREATE TABLE reconciliations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id     TEXT        NOT NULL,
    homebox_qty NUMERIC     NOT NULL,
    counted_qty NUMERIC     NOT NULL,
    delta       NUMERIC     NOT NULL,
    applied     BOOLEAN     NOT NULL,
    note        TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reconciliations_item_idx ON reconciliations (item_id, created_at);

-- +goose Down
DROP TABLE reconciliations;
DROP TABLE events;
DROP TABLE item_codes;
