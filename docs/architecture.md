# Architecture

## Code layout

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

## Storage

- **Homebox** is the source of truth for items and their quantity. The ledger writes the resulting quantity back to Homebox after every event.
- **Postgres** holds only data that exists nowhere else: `events`, `reconciliations` and `item_codes`. `item_codes` stores code-to-item links that were learned or added manually.
- **Qdrant** stores one point per Homebox item. Each point has its vectors plus a payload with the name, tags, location and normalized codes, and a content hash.
- There is no item cache: names and category membership are read live from Homebox.

## Matching

Item lookup accepts an EAN/UPC/GTIN, a vendor's own SKU, or a receipt line like `BARILLA SPAG 0,5KG`. Exact codes are tried first, then hybrid search (embeddings plus BM25-style lexical matching, fused with RRF).

1. The code is normalized: UPC-A, GTIN-14 and UPC-E become EAN-13 when the check digit is valid; any other code is upper-cased with separators stripped.
2. The service looks for a learned or manual link for the vendor, then for the code with no vendor.
3. It then looks for codes stored on Homebox items: the custom fields named in `homebox.code_fields` (default `EAN`, `UPC`, `Barcode`, `GTIN`, `SKU`), plus model number, serial number and asset ID.
4. If neither matches, it runs a hybrid search on the text (or on the code itself when no text is given). The result is `confident` when the top hit's cosine similarity is at least `matching.min_similarity` and leads the runner-up by `matching.min_margin`.

`POST /api/v1/codes`, or `POST /api/v1/scan` with `"learn": true`, links a vendor code to an item, so the next scan of that code is an exact match.

The text that gets embedded for each item is built from `internal/service/catalog/templates/document.tmpl`.

## Sync

On startup and then every `sync.interval`, the service lists Homebox items and fetches details only for items whose `updatedAt` changed. It re-embeds an item only when its content hash changed, so quantity updates don't trigger re-embedding. Points for items deleted or archived in Homebox are removed. Changing the embedding model re-embeds everything. Changing the vector dimension requires deleting the collection, and startup will tell you so.

## Ledger and reconciliation

- Every `purchase`, `consume` or `adjust` is stored as an event, and the new quantity is pushed to Homebox.
- Reconciliation compares a physical count with the quantity in Homebox, for one item or as a batch stock-take. With `apply: true` the difference is recorded and written back.

## Charts

`/api/v1/charts` returns JSON time series of consumption, purchases, spend or stock level per item, per category (Homebox tag) or as a total. Buckets can be daily, weekly, monthly, quarterly or yearly.

## Observability

Traces (HTTP, pgx, Qdrant gRPC, embeddings HTTP and domain spans), logs with trace IDs and the `hba_*` metrics are exported over OTLP. The local compose stack includes Grafana (otel-lgtm) at http://localhost:3000.
