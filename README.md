# Homebox Analytics

**Know how fast you go through things, not just how many you have left.**

[Homebox](https://github.com/sysadminsmedia/homebox) is great at telling you what you own and where it is. It's less helpful for the things you use up: coffee, pasta, detergent, filters, batteries.

## The problem

Homebox only stores the *current* quantity of an item. When you change it, the old value is gone. That means it can't answer questions like:

- How much coffee do we drink in a month?
- When will we run out of dishwasher tablets?
- How much did we spend on groceries this quarter, and on what?
- Is the shelf count actually what Homebox says it is?

Homebox also has no hooks or events for item updates, so other tools (a barcode scanner, a receipt parser, an automation) can't react to changes or record *why* a quantity changed.

## What Homebox Analytics adds

Homebox Analytics runs next to your Homebox and becomes the place where stock changes go through.

- **A history of every change.** Each purchase, use and correction is recorded as an event with a time, amount and price. Homebox is kept up to date automatically, so it stays your single source of truth for what's on the shelf.
- **Scan or paste, it finds the item.** Send a barcode, a store's own product code, or a messy receipt line like `BARILLA SPAG 0,5KG`, and it figures out which Homebox item you mean. When it's unsure, it shows you the candidates, and it remembers your answer for next time.
- **Stock-takes.** Count what's actually on the shelf, compare it with Homebox, and fix the differences in one go.
- **Consumption and spending charts.** See usage, purchases, spend and stock levels over time, per item, per category (Homebox tag) or across everything, by day, week, month, quarter or year.
- **An API for your automations.** Barcode scanners, receipt OCR, Home Assistant or n8n flows can record events through a simple HTTP API instead of editing quantities by hand.

## Quick start

You need Docker and a running Homebox (v0.27 or newer).

```bash
cd deployments/docker/production
cp .env.example .env    # set HBA_HOMEBOX_URL, Homebox credentials and POSTGRES_PASSWORD
docker compose up -d
curl localhost:8080/readyz
```

This starts the service with everything it needs (Postgres, Qdrant and a local Ollama for item matching). Make sure `HBA_HOMEBOX_URL` points to an address reachable from inside the container, not `localhost`.

See [docs/deployment.md](docs/deployment.md) for configuration options and image tags.

## Documentation

- [Deployment](docs/deployment.md): production setup and configuration
- [API](docs/api.md): endpoints and the OpenAPI contract
- [Architecture](docs/architecture.md): components, storage, matching and sync in detail
- [Development](docs/development.md): running locally, tests and tooling

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

See [LICENSE.md](LICENSE.md).
