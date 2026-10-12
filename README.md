# trading212-mcp-server

A [Model Context Protocol](https://modelcontextprotocol.io) server for the
[Trading 212](https://www.trading212.com) public API, written in Go. It lets
MCP clients such as Claude Desktop and Cursor read your account, positions,
orders, pies, history and instrument metadata, and (when you allow it) place
and cancel orders, manage pies and request CSV exports.

Built on the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

> **Warning:** order and pie tools act on a real brokerage account when
> `ENVIRONMENT=live`. Start with the demo (paper trading) environment, give your
> API key only the permissions you need, and review every write action your
> assistant proposes.

## Features

### Tools

Read-only tools are annotated `readOnlyHint: true`. Tools that change account
state are annotated `destructiveHint: true` so clients can ask for confirmation.

| Area | Tool | Access |
| --- | --- | --- |
| Account | `fetch_account_info`, `fetch_account_summary` | read |
| | `fetch_account_cash` | read |
| | `fetch_positions` (optional `ticker`), `fetch_all_open_positions` | read |
| | `fetch_position_by_ticker`, `fetch_open_position_by_ticker`, `search_specific_position_by_ticker` | read |
| Orders | `fetch_all_orders`, `fetch_order` | read |
| | `place_market_order`, `place_limit_order`, `place_stop_order`, `place_stop_limit_order` | write |
| | `cancel_order` | write |
| Pies | `fetch_pies`, `fetch_a_pie` | read |
| | `create_pie`, `update_pie`, `duplicate_pie`, `delete_pie` | write |
| History | `fetch_historical_order_data`, `fetch_paid_out_dividends`, `fetch_transaction_list` | read |
| | `fetch_exports_list` | read |
| | `request_csv_export` | write |
| Metadata | `search_instrument`, `search_exchange` | read |

Notes:

- Order quantities are positive to buy and negative to sell. Limit and stop
  orders accept `time_validity` of `DAY` (default) or `GOOD_TILL_CANCEL`.
- History tools are cursor-paginated: `limit` is 1–50 (default 20). To get the
  next page, pass the previous page's `nextPagePath` back as `next_page_path`
  (the approach the API docs recommend); a `null` `nextPagePath` means the last
  page. Only paths for the same history endpoint are accepted.
- Trading 212 has deprecated the pies endpoints. The pie tools still work but
  say so in their descriptions.
- Order placement is not idempotent upstream, and orders execute only in the
  account's primary currency. The API only supports Invest and Stocks ISA
  accounts.
- Timestamps (`time_from`, `time_to`, `end_date`) must be ISO 8601 with a
  timezone, e.g. `2024-01-01T00:00:00Z`.
- Every tool returns typed structured content with an output schema. List
  results are wrapped in an object (for example `{"positions": [...]}`).

### Resources

JSON resources for clients that browse context directly:

- `trading212://account/info`, `trading212://account/summary`, `trading212://account/cash`
- `trading212://positions`, `trading212://positions/{ticker}`
- `trading212://orders`, `trading212://orders/{order_id}`
- `trading212://pies`, `trading212://pies/{pie_id}`
- `trading212://instruments`, `trading212://exchanges`
- `trading212://history/exports`

Aliases: `trading212://account/positions`, `trading212://account/portfolio`
and their `/{ticker}` forms.

### Prompt

`analyse_trading212_data` gives the model guidance for analysing account data
(observed data versus assumptions, GBX is pence) and includes the account
currency when it can be fetched.

### Caching and rate limits

Trading 212 enforces tight per-endpoint rate limits, so GET responses are
cached in memory:

| Endpoint family | Default TTL |
| --- | --- |
| Account, positions, orders, pies, export status | 15 s |
| Order, dividend and transaction history | 300 s |
| Instruments and exchanges | 3600 s |

For reference, Trading 212's documented limits (per account, across all keys)
include: account summary and active orders 1 req / 5 s, positions and single
order 1 req / 1 s, history 20 req / min, export list 1 req / min, export
request 1 req / 30 s, instruments 1 req / 50 s, exchanges and pie list
1 req / 30 s, limit/stop orders 1 req / 2 s, market orders and cancellations
50 req / min.

Any write (order, pie, export, cancel) invalidates all cached account data,
even if the write fails, because it may still have been applied upstream.
Each tool result and resource carries `_meta["io.github.netf.trading212/cache"]`
with `cacheHit`, `retrievedAt` and `ttlSeconds` for every upstream call, so the
model can tell how fresh the data is.

GET requests are retried up to three times within a 30 second budget on
connection errors, `408`, `429` and `5xx`, honouring `Retry-After` and
`x-ratelimit-reset`. Writes are never retried automatically; if a write times
out or fails with a server error, the error says the outcome is unknown.
Errors distinguish rejected credentials (`401`), a key missing the required
permission scope (`403`), not found, rate limited and timeouts. Messages
contain the HTTP status and Trading 212's error code, but never credentials or
raw response bodies.

## Setup

### 1. Get API credentials

In the Trading 212 app open **Settings → API (Beta)**, generate an API key for
the demo or live account, and select only the permissions you need (for
example, leave out `orders:execute` and `pies:write` if you don't want the
assistant to trade). A call that needs a scope the key lacks fails with a
permission error. Newer keys
come as a key + secret pair; older keys are a single value. See Trading 212's
[API key guide](https://helpcentre.trading212.com/hc/en-us/articles/14584770928157-Trading-212-API-key)
and the [API reference](https://docs.trading212.com/api).

### 2. Install

With Go 1.25 or later:

```bash
go install github.com/netf/trading212-mcp-server/cmd/trading212-mcp-server@latest
```

Or build from a checkout:

```bash
git clone https://github.com/netf/trading212-mcp-server.git
cd trading212-mcp-server
make build            # produces ./bin/trading212-mcp-server
```

### 3. Configure

Configuration comes from environment variables. The server also reads a `.env`
file from its working directory (override with `-env-file PATH`); real
environment variables always take precedence.

```bash
cp .env.example .env   # then edit it
```

| Variable | Default | Description |
| --- | --- | --- |
| `TRADING212_API_KEY` | — (required) | API key |
| `TRADING212_API_SECRET` | empty | API secret. When set, requests use HTTP Basic auth with `key:secret`; otherwise the key is sent as-is |
| `ENVIRONMENT` | `demo` | `demo` (paper trading) or `live` |
| `TRANSPORT` | `stdio` | `stdio`, `sse` or `streamable-http` |
| `TRADING212_HTTP_ADDR` | `127.0.0.1:8000` | Listen address for the HTTP transports |
| `TRADING212_ACCOUNT_TTL` | `15` | Cache seconds for account, positions, orders, pies and export status |
| `TRADING212_HISTORY_TTL` | `300` | Cache seconds for history endpoints |
| `TRADING212_METADATA_TTL` | `3600` | Cache seconds for instruments and exchanges |

TTLs are non-negative seconds (fractions allowed); `0` disables caching for
that family.

Flags: `-env-file PATH`, `-v` (debug and protocol logs), `-version`. Logs
always go to stderr so they never corrupt the stdio transport.

## Connecting a client

### Claude Desktop

Edit `claude_desktop_config.json` (macOS:
`~/Library/Application Support/Claude/claude_desktop_config.json`, Windows:
`%APPDATA%\Claude\claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "trading212": {
      "command": "/absolute/path/to/trading212-mcp-server",
      "env": {
        "TRADING212_API_KEY": "your-api-key",
        "TRADING212_API_SECRET": "your-api-secret",
        "ENVIRONMENT": "demo"
      }
    }
  }
}
```

`go install` puts the binary in `$(go env GOPATH)/bin`. Restart Claude Desktop
after editing the file.

### Cursor

Add the server to `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (per
project):

```json
{
  "mcpServers": {
    "trading212": {
      "command": "/absolute/path/to/trading212-mcp-server",
      "env": {
        "TRADING212_API_KEY": "your-api-key",
        "TRADING212_API_SECRET": "your-api-secret",
        "ENVIRONMENT": "demo"
      }
    }
  }
}
```

To keep secrets out of the client config, omit `env` and pass
`"args": ["-env-file", "/absolute/path/to/.env"]` instead.

### HTTP transports

```bash
TRANSPORT=streamable-http ./bin/trading212-mcp-server   # http://127.0.0.1:8000/mcp
TRANSPORT=sse ./bin/trading212-mcp-server               # http://127.0.0.1:8000/sse
```

For example, in Cursor: `{"mcpServers": {"trading212": {"url": "http://127.0.0.1:8000/mcp"}}}`.

The HTTP endpoints have no authentication of their own; anyone who can reach
them can use your Trading 212 key. Keep the default loopback address. Requests
whose `Host` header isn't localhost and cross-origin browser requests are
rejected.

### Docker

```bash
make docker   # or: docker build -t trading212-mcp-server .
docker run --rm -i --env-file .env trading212-mcp-server
```

As a desktop client command:

```json
{
  "mcpServers": {
    "trading212": {
      "command": "docker",
      "args": ["run", "--rm", "-i",
               "-e", "TRADING212_API_KEY", "-e", "TRADING212_API_SECRET", "-e", "ENVIRONMENT",
               "trading212-mcp-server"],
      "env": {
        "TRADING212_API_KEY": "your-api-key",
        "TRADING212_API_SECRET": "your-api-secret",
        "ENVIRONMENT": "demo"
      }
    }
  }
}
```

## Development

```bash
make test        # go test ./...
make test-race   # with the race detector
make vet         # go vet ./...
make lint        # golangci-lint (v2) using .golangci.yml
make check       # vet + lint + race tests
```

Layout:

```
cmd/trading212-mcp-server/   entry point: flags, config, transports
internal/config/             environment and .env loading and validation
internal/trading212/         typed API client: models, auth, retries, caching, errors
internal/server/             MCP tools, resources and prompt
```

The API client is tested against `httptest` servers, and the MCP layer is
tested end-to-end through an in-memory MCP client session. No test calls
Trading 212.

Contract tests check the client against a snapshot of the official OpenAPI
document in `internal/trading212/testdata/openapi.yaml`. They verify that every
documented operation is implemented, that no undocumented endpoint or query
parameter is used, that every schema field matches the Go models in name and
type (in both directions), that enums match, and that servers and auth schemes
are unchanged.

```bash
make check-spec    # diff the published spec against the snapshot
make update-spec   # refresh the snapshot and rerun the contract tests
```

A weekly workflow (`.github/workflows/spec-drift.yml`) runs `make check-spec`.

## License

[MIT](LICENSE)
