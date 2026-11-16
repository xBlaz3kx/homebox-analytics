# Deployment

## Docker Compose (production)

[`deployments/docker/production`](../deployments/docker/production) runs the published image (`ghcr.io/xblaz3kx/homebox-analytics`) with Postgres, Qdrant and optionally Ollama. It expects Homebox (>= 0.27) to already be running elsewhere.

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

## Configuration

Settings are in [`config/service.config.yaml`](../config/service.config.yaml). Any key can be overridden with an env var, e.g. `HBA_EMBEDDINGS_BASE_URL` for `embeddings.base_url`.
