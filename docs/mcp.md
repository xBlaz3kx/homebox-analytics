# MCP server

The service has a built-in [Model Context Protocol](https://modelcontextprotocol.io) server, so LLM clients (Claude, n8n AI agents, Home Assistant and others) can manage the inventory in plain language: "we finished a bag of rice", "I bought 3 coffees at 4.20 each", "how much detergent did we use this year?".

It is served over Streamable HTTP (stateless, JSON responses) on the same port as the REST API, at `/mcp` by default.

## Connecting

Claude Code:

```bash
claude mcp add --transport http homebox http://<host>:8080/mcp
```

Any other client that supports Streamable HTTP takes the same URL, for example n8n's *MCP Client Tool* node or the MCP Inspector (`npx @modelcontextprotocol/inspector`).

## Tools

| Tool | What it does |
|---|---|
| `search_items` | Search Homebox items by name/keyword and tags |
| `get_item` | Full item details plus its linked product codes |
| `list_tags` | List Homebox tags (categories) |
| `lookup_item` | Resolve a barcode, vendor code or receipt line to candidate items |
| `record_event` | Record a `purchase` / `consume` / `adjust` and update the Homebox quantity |
| `scan` | Resolve and record in one step; returns candidates instead if the match isn't confident |
| `list_events` | Stock event history of an item |
| `reconcile` | Stock-take: preview differences, or apply counted quantities |
| `list_reconciliations` | Past stock-takes |
| `link_code` / `list_codes` / `delete_code` | Manage product code links |
| `get_chart` | Usage, purchases, spend or stock level over time |
| `sync_catalog` | Re-index Homebox items for lookup now |

Read-only tools carry the `readOnlyHint` annotation; `reconcile` and `delete_code` are marked destructive, so clients that ask for confirmation will do so there.

Quantities only change through the ledger (`record_event`, `scan`, `reconcile`), so every change made by an assistant ends up in the history. Creating, editing or deleting Homebox items, locations and tags is not exposed; do that in Homebox.

The server instructions and tool descriptions are templates in [`internal/service/mcp/templates`](../internal/service/mcp/templates).

## Configuration

```yaml
mcp:
  enabled: true   # HBA_MCP_ENABLED
  path: "/mcp"    # HBA_MCP_PATH
```

## Security

Like the REST API, `/mcp` has no authentication of its own, and its tools can change quantities in Homebox. Only expose it on a trusted network, or put it behind a reverse proxy that handles authentication. Set `mcp.enabled: false` if you don't need it.
