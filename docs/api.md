# API

The full contract is in [`api/openapi.yaml`](../api/openapi.yaml).

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/items/lookup?code=&vendor=&q=&limit=` | Resolve a code and/or text to candidates |
| POST | `/api/v1/scan` | Resolve and record an event in one call (409 with candidates if not confident) |
| POST/GET | `/api/v1/items/{id}/events` | Record a `purchase` / `consume` / `adjust`, or list events |
| POST | `/api/v1/items/{id}/reconcile` | Compare a count with Homebox (`apply: true` to write it) |
| POST | `/api/v1/reconcile` | Batch stock-take |
| GET | `/api/v1/reconciliations` | Reconciliation history |
| POST/DELETE | `/api/v1/codes`, `/api/v1/codes/{id}` | Manage code links; `GET /api/v1/items/{id}/codes` lists them |
| GET/POST | `/api/v1/charts` | Time series per item, category or total |
| POST | `/api/v1/sync` | Sync now |
| GET | `/healthz`, `/readyz` | Liveness / dependency readiness |
| POST | `/mcp` | MCP server for LLM clients, see [mcp.md](mcp.md) |

See [architecture.md](architecture.md) for how lookups are matched and how quantities flow back to Homebox.
