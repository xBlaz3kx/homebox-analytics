# Development

## Running locally

The development compose file starts every dependency, including a throwaway Homebox.

```bash
cd deployments/docker
docker compose --profile homebox up -d            # postgres, qdrant, ollama (+ model pull), otel-lgtm, homebox
# create a user and items in Homebox at http://localhost:7745, then:
HBA_HOMEBOX_USERNAME=you@example.com HBA_HOMEBOX_PASSWORD=... docker compose up -d --build service
curl localhost:8080/readyz
```

- **Config:** settings are in [`config/service.config.yaml`](../config/service.config.yaml). Any key can be overridden with an env var, e.g. `HBA_EMBEDDINGS_BASE_URL` for `embeddings.base_url`.
- **Bifrost:** run `docker compose --profile bifrost up -d bifrost`, then set `embeddings.base_url: http://bifrost:8080/v1` and `embeddings.model: ollama/nomic-embed-text`.
- **Observability:** Grafana is at http://localhost:3000. It shows traces, logs with trace IDs, and the `hba_*` metrics.

## Tests and tooling

```bash
go test ./...                                                   # unit tests
go test -tags integration -run TestEndToEnd ./internal/service/ # Postgres + Qdrant via testcontainers
golangci-lint run ./...
mockery                                                         # regenerate mocks (internal/service/*/mocks)
```
