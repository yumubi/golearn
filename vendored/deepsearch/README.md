# DeepSearch

[简体中文](./README_ZH.md) · English

DeepSearch wraps a DeepSeek model and Anthropic's Server Tool Use `web_search` into an LLM-friendly HTTP API and MCP server: one query in, a written answer out, together with the source URLs it cites and the token usage it cost.

The model runs through any Anthropic-compatible endpoint, the `web_search` tool executes server-side at the provider, and `WebFetch` reads individual public pages locally when a snippet is not enough.

## Quick Start

```bash
go generate ./...        # regenerate .env.example from pkg/env
cp .env.example .env     # fill in MODEL_API_KEY / MODEL_BASE_URL / MODEL
go run . server -d .env  # listens on 0.0.0.0:8231 by default
```

Running without a subcommand enters `server` as well. `MODEL_BASE_URL` must speak the Anthropic Messages API and support the server-side `web_search` tool; `MODEL` is the model ID behind it, for example `deepseek-flash`.

## Container

```bash
docker run -d --name deepsearch -p 8231:8231 -v "$PWD/.env:/app/.env:ro" ghcr.io/clarkqaq/deepsearch:latest
```

Or with Compose, which mounts `.env` and inherits the image health check:

```bash
docker compose pull && docker compose up -d
```

Every `v*` tag publishes `linux/amd64` images to `ghcr.io/clarkqaq/deepsearch`, tagged `<version>`, `<major>.<minor>` and `latest`; a new GHCR package starts private, so make it public in the repository settings when anonymous pulls are wanted.

The image runs as the unprivileged `deepsearch` user and polls `/healthz`. Configuration is a `.env` file mounted at `/app/.env`, and `APP_`-prefixed variables work just as well. The listen port comes from `HTTP_ADDR` in that config, so keep `DEEPSEARCH_PORT` in sync with it — for a `.env` that sets `HTTP_ADDR=0.0.0.0:8232` use `DEEPSEARCH_PORT=8232 docker compose up -d`. Building the image from source is documented in [AGENTS.md](AGENTS.md).

## Build and Test

```bash
go build ./...
go vet ./...
go test -race ./...
```

## REST API

```bash
curl -s http://127.0.0.1:8231/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"What changed in Go 1.27?"}' | jq
```

```json
{
  "answer": "...(plain-text answer with source URLs)...",
  "sources": [{"url": "https://go.dev/doc/go1.27", "title": "Go 1.27 Release Notes"}],
  "usage": {
    "input_tokens": 12000,
    "output_tokens": 800,
    "total_tokens": 12800,
    "reasoning_tokens": 0,
    "cache_creation_tokens": 0,
    "cache_read_tokens": 0
  },
  "model": "deepseek-flash",
  "duration_ms": 18432
}
```

```bash
curl -s http://127.0.0.1:8231/v1/fetch \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://go.dev/doc/go1.27"}' | jq
```

```json
{
  "url": "https://go.dev/doc/go1.27",
  "title": "Go 1.27 Release Notes",
  "text": "...(visible page text)...",
  "links": [{"url": "https://go.dev/", "text": "The Go Programming Language"}],
  "truncated": false
}
```

Errors always come back in the same envelope:

```json
{"error": {"code": "invalid_request", "message": "query is required"}}
```

| HTTP | code | When |
| --- | --- | --- |
| 400 | `invalid_request` | Malformed body, empty query, or an unusable URL |
| 401 | `unauthorized` | A token is configured but missing or wrong |
| 404 | `fetch_disabled` | `/v1/fetch` called while `WEB_FETCH_ENABLED=false` |
| 502 | `search_failed` / `fetch_failed` | Search failed, or the page could not be read |
| 500 | `internal` | Any other internal failure |

## Capabilities

| Endpoint | Description |
| --- | --- |
| `POST /v1/search` | One synchronous search: answer, sources, token usage, duration |
| `POST /v1/fetch` | Read one public web page: title, text, outgoing links |
| `GET /healthz` | Health check, always unauthenticated |
| `POST /mcp` | MCP Streamable HTTP, exposing the `web_search` tool |

Synchronous only: no streaming responses and no background jobs.

## Configuration

The full list lives in [.env.example](.env.example), generated from `pkg/env.Env` by `go generate ./...`.

| Variable | Default | Description |
| --- | --- | --- |
| `MODEL_API_KEY` | empty | Model API key, required |
| `MODEL_BASE_URL` | empty | Model API base URL |
| `MODEL` | empty | Model ID, required, for example `deepseek-flash` |
| `MODEL_OUTPUT_LIMIT` | empty | Maximum output tokens of one answer |
| `WEB_SEARCH_MAX_USES` | `10` | Maximum `web_search` server-tool calls within one request |
| `WEB_FETCH_ENABLED` | `true` | Enables `WebFetch`, which also gates `POST /v1/fetch` |
| `WEB_FETCH_TIMEOUT` | `15s` | Timeout of one fetch, including DNS, dialing, redirects and body read |
| `WEB_FETCH_MAX_BYTES` | `2097152` | Maximum bytes read from one page |
| `WEB_FETCH_MAX_CHARS` | `20000` | Maximum characters returned to the model |
| `HTTP_ADDR` | `0.0.0.0:8231` | Listen address of the REST API and the MCP endpoint |
| `AUTH_TOKEN` | empty | Optional bearer token shared by REST and MCP; when set, every request except `/healthz` needs `Authorization: Bearer <token>` |
| `MAX_HEADER_BYTES` | `2097152` | Maximum size of HTTP request headers |
| `MAX_MULTIPART_MEMORY` | `67108864` | Maximum memory for parsing multipart forms before spilling to disk |
| `READ_TIMEOUT` | `120s` | Maximum duration for reading a whole request, including its body |
| `WRITE_TIMEOUT` | `300s` | Maximum duration before timing out response writes; it must cover a whole search |
| `IDLE_TIMEOUT` | `30s` | How long idle connections are kept open |
| `STD_LOG_LEVEL` | `info` | Console log level |

Variables in a `.env` file carry no prefix; when they come from the process environment they need the `APP_` prefix, for example `APP_MODEL_API_KEY`.

## MCP

The MCP endpoint exposes a single tool, `web_search`. It takes `{"query": "..."}` and returns the same shape as `/v1/search`, also as structured output.

```json
{
  "mcpServers": {
    "deepsearch": {
      "type": "http",
      "url": "http://127.0.0.1:8231/mcp",
      "headers": {"Authorization": "Bearer <AUTH_TOKEN>"}
    }
  }
}
```

Omit `headers` when `AUTH_TOKEN` is not set.

## Architecture

```
cmd/                       cobra: root (no subcommand → server), server (assembles providers, serves HTTP)
internal/provider/         DI wiring: env → logger → llm → search → http
internal/service/search/   search domain: Search (one agent run), Fetch (one page)
internal/transport/        registers REST routes and /mcp on provider.Http (MCP Streamable HTTP)
pkg/agentutil/             WebSearch agent, WebFetch/GetTime tools, system prompts
pkg/di, pkg/env, pkg/httperr, pkg/slogtrap, pkg/util
```

Dependencies point inward: `cmd` → `internal/provider` → `internal/service` → `pkg/*`, and `internal/transport` depends only on `internal/service`. Services are resolved by type from the `pkg/di` container, and `provider.Setup` registers them all.

The echo server is built once in `internal/provider/http.go` (`echo.NewWithConfig` + `StartConfig`: read/write timeouts, maximum request header size, multipart memory limit, `BaseContext`); `internal/transport` only mounts middleware and routes on that shared instance, while `cmd` starts it with `StartConfig.Start(ctx, http)`. Every handler error is rendered by `pkg/httperr` into the uniform envelope above, and internal error text is exposed only when `STD_LOG_LEVEL=debug`.

## Security

`WebFetch` (including `/v1/fetch`) only accepts public domain names and public addresses: literal IP addresses, `localhost`, private, CGNAT, link-local, multicast, reserved and benchmark ranges are all refused, every redirect hop is re-validated, and the resolved peer address is checked again right before the socket is dialed to defeat DNS rebinding.

## License

Released under the MIT License. See [LICENSE](LICENSE) for the full text.
