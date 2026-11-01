# Homebox Analytics

Tracks how fast consumables in [Homebox](https://github.com/sysadminsmedia/homebox) get used up.

- **Find items by code or text.** Looks up an EAN/UPC/GTIN, a vendor's own SKU, or a receipt line like `BARILLA SPAG 0,5KG`. It tries exact codes first, then falls back to hybrid search (embeddings plus BM25-style lexical matching, fused with RRF).
- **Ledger.** Records purchases, consumption and adjustments, and writes the resulting quantity back to Homebox. Homebox stays the source of truth for quantity.
- **Reconciliation.** Compares a stock count with Homebox, one item at a time or as a batch stock-take, and can apply the difference.
- **Charts.** Returns JSON time series of consumption, purchases, spend or stock level per item, per category (Homebox tag) or as a total. Buckets can be daily, weekly, monthly, quarterly or yearly.

## Architecture

```
cmd/service                 entrypoint (cobra), dependency wiring, config file + HBA_* env vars
internal/pkg/               reusable, domain-agnostic
  homebox                   Homebox REST client (entities/tags API, >= 0.27)
  embeddings                OpenAI-compatible /v1/embeddings client (Ollama, Bifrost, OpenAI, ...)
  qdrant                    search.Index on Qdrant: named dense + IDF sparse vectors, RRF fusion
  search                    Embedder/Index interfaces, sparse encoder, HybridSearch
  barcode                   GTIN normalization (EAN-8/13, UPC-A/E, GTIN-14) and code cleanup
  database                  pgx pool (OTel traced) + embedded goose migrations
  observability             OTel traces/metrics/logs over OTLP, zap bridge
internal/service/           domain
  catalog                   matching, code links, Homebox -> Qdrant sync
  ledger                    events, reconciliation, quantity push to Homebox
  analytics                 chart queries
  api                       HTTP handlers
```

Storage:
- **Postgres** holds only data that exists nowhere else: `events`, `reconciliations` and `item_codes`. `item_codes` stores code-to-item links that were learned or added manually.
- **Qdrant** stores one point per Homebox item. Each point has its vectors plus a payload with the name, tags, location and normalized codes, and a content hash.
- There is no item cache: names and category membership are read live from Homebox.

### Matching

1. The code is normalized: UPC-A, GTIN-14 and UPC-E become EAN-13 when the check digit is valid; any other code is upper-cased with separators stripped.
2. The service looks for a learned or manual link for the vendor, then for the code with no vendor.
3. It then looks for codes stored on Homebox items: the custom fields named in `homebox.code_fields` (default `EAN`, `UPC`, `Barcode`, `GTIN`, `SKU`), plus model number, serial number and asset ID.
4. If neither matches, it runs a hybrid search on the text (or on the code itself when no text is given). The result is `confident` when the top hit's cosine similarity is at least `matching.min_similarity` and leads the runner-up by `matching.min_margin`.

`POST /api/v1/codes`, or `POST /api/v1/scan` with `"learn": true`, links a vendor code to an item, so the next scan of that code is an exact match.

### Sync

On startup and then every `sync.interval`, the service lists Homebox items and fetches details only for items whose `updatedAt` changed. It re-embeds an item only when its content hash changed, so quantity updates don't trigger re-embedding. Points for items deleted or archived in Homebox are removed. Changing the embedding model re-embeds everything. Changing the vector dimension requires deleting the collection, and startup will tell you so.

## API

The full contract is in [`api/openapi.yaml`](api/openapi.yaml).

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/items/lookup?code=&vendor=&q=&limit=` | Resolve a code and/or text to candidates |
| POST | `/api/v1/scan` | Resolve and record an event in one call (409 with candidates if not confident) |
| POST/GET | `/api/v1/items/{id}/events` | Record a `purchase` / `consume` / `adjust`, or list events |
| POST | `/api/v1/items/{id}/reconcile` | Compare a count with Homebox (`apply: true` to write it) |
| POST | `/api/v1/reconcile` | Batch stock-take |
| GET | `/api/v1/reconciliations` | Reconciliation history |
| POST/DELETE | `/api/v1/codes`, `/api/v1/codes/{id}` | Manage code links; `GET /api/v1/items/{id}/codes` lists them |
| GET/POST | `/api/v1/charts` | Time series (see below) |
| POST | `/api/v1/sync` | Sync now |
| GET | `/healthz`, `/readyz` | Liveness / dependency readiness |

### Charts

```
GET /api/v1/charts?items=<id,...>&categories=<tagId,...|all>
                  &group_by=item|category|total
                  &metric=consumed|purchased|spend|quantity|events
                  &interval=day|week|month|quarter|year
                  &from=2026-01-01&to=2026-12-31&timezone=Europe/Ljubljana
```

- **Scope** is `items` plus the current members of `categories`. `categories=all` selects every tag and defaults to one series per category.
- **`consumed`** includes negative reconciliation deltas, i.e. stock found missing at a count.
- **`quantity`** carries the last known level forward through buckets with no events.
- **Multiple tags:** with `group_by=category`, an item that has several selected tags counts toward each of them.

```bash
# Monthly consumption per category
curl 'localhost:8080/api/v1/charts?categories=all&interval=month'
# Quarterly spend for two items
curl 'localhost:8080/api/v1/charts?items=<a>,<b>&metric=spend&interval=quarter'
```

## Running locally

```bash
cd deployments/docker
docker compose --profile homebox up -d            # postgres, qdrant, ollama (+ model pull), otel-lgtm, homebox
# create a user and items in Homebox at http://localhost:7745, then:
HBA_HOMEBOX_USERNAME=you@example.com HBA_HOMEBOX_PASSWORD=... docker compose up -d --build service
curl localhost:8080/readyz
```

- **Config:** settings are in [`config/service.config.yaml`](config/service.config.yaml). Any key can be overridden with an env var, e.g. `HBA_EMBEDDINGS_BASE_URL` for `embeddings.base_url`.
- **Bifrost:** run `docker compose --profile bifrost up -d bifrost`, then set `embeddings.base_url: http://bifrost:8080/v1` and `embeddings.model: ollama/nomic-embed-text`.
- **Observability:** Grafana is at http://localhost:3000. It shows traces (HTTP, pgx, Qdrant gRPC, embeddings HTTP and domain spans), logs with trace IDs, and the `hba_*` metrics.

## Deploying with Docker

[`deployments/docker/production`](deployments/docker/production) runs the published image (`ghcr.io/xblaz3kx/homebox-analytics`) with Postgres, Qdrant and optionally Ollama. It expects Homebox to already be running elsewhere.

```bash
cd deployments/docker/production
cp .env.example .env    # set HBA_HOMEBOX_URL, Homebox credentials and POSTGRES_PASSWORD
docker compose up -d
curl localhost:8080/readyz
```

- **Configuration:** everything is set through `.env`. Postgres and Qdrant aren't published on host ports, and data is kept in named volumes.
- **Homebox URL:** `HBA_HOMEBOX_URL` must be reachable from inside the container, so `localhost` won't work.
- **Embeddings:** `COMPOSE_PROFILES=ollama` (the default) runs Ollama and pulls the model. To use an existing Ollama, Bifrost or OpenAI endpoint instead, clear that line and set `HBA_EMBEDDINGS_BASE_URL`.
- **Image tags:** CI publishes `latest` from `main` and semver tags from `v*` tags. Pin `HBA_IMAGE_TAG` to a version for repeatable deploys.

## Development

```bash
go test ./...                                                   # unit tests
go test -tags integration -run TestEndToEnd ./internal/service/ # Postgres + Qdrant via testcontainers
golangci-lint run ./...
mockery                                                         # regenerate mocks (internal/service/*/mocks)
```

The text that gets embedded for each item is built from `internal/service/catalog/templates/document.tmpl`.
