# AGENTS.md

This file provides guidance to coding agents working in this repository.

## Build, Run, Test

```bash
# Build
go build -o ./deepsearch -trimpath .

# Run (loads .env by default)
go run . server -d .env

# Verify after any code change
go build ./... && go vet ./... && go test ./...

# Regenerate .env.example from pkg/env.Env
go generate ./...

# Build the image from source (multi-stage)
docker build --build-arg BUILD_VERSION="$(git describe --tags --always)" \
  -t ghcr.io/clarkqaq/deepsearch:local .

# Run the published image with Compose (mounts ./.env at /app/.env)
docker compose pull && docker compose up -d

# Run a local build with Compose instead
DEEPSEARCH_IMAGE=ghcr.io/clarkqaq/deepsearch:local docker compose up -d --build
```

Never consider work complete until `go build ./...` and `go vet ./...` pass cleanly.

## Container and CI

The `Dockerfile` builds from source in two stages: `golang:1.27-alpine` compiles a static binary, and `alpine:3.24` runs it as the unprivileged `deepsearch` user with `ca-certificates`, `Asia/Shanghai` time, the `scripts/healthcheck.sh` probe, and `/app/.env` as the default config path. Every dependency is public, so the build needs no credentials.

`BUILD_VERSION` / `BUILD_COMMIT` / `BUILD_DATE` are the image build args; `BUILD_VERSION` is injected as `-X deepsearch/pkg/util.version`, which `util.BuildVersion()` prefers over the VCS revision. Keep `.dockerignore` in sync when adding files: `.env` must never enter the build context, and `*.md` must never be excluded globally because `pkg/agentutil/prompt/*.md` is embedded with `go:embed`.

Two workflows live in `.github/workflows`: `ci.yml` runs gofmt, the `.env.example` drift check (`go generate` + `git diff`), `go mod verify`, build, vet and `go test -race`, plus a build-only container job; `container.yml` publishes `linux/amd64` images to `ghcr.io/${GITHUB_REPOSITORY,,}` (resolved: `ghcr.io/clarkqaq/deepsearch`) on `v*` tags with semver/`latest` tags, provenance and SBOM, and tags manual runs as `edge`. Image names must stay lowercase, and neither workflow needs repository secrets.

## Releasing

Releases are cut from a green `master` and the tag is the only version source:

```bash
git tag -a v0.1.1 -m "DeepSearch v0.1.1"
git push origin v0.1.1
```

Pushing a `v*` tag runs `container.yml`, which publishes `linux/amd64` images to `ghcr.io/clarkqaq/deepsearch` tagged `<version>`, `<major>.<minor>` and `latest`. The tag name becomes `BUILD_VERSION`, so `util.BuildVersion()` reports it over MCP; bump the patch for docs or container-only changes and the minor for new capability. Afterwards confirm the run is green and that all four tags share one digest, then check the package is public if anonymous pulls are expected. No GitHub Release page is created automatically.

## High-Level Architecture

deepsearch is a small Go (1.27) service that exposes web search over a REST API and an MCP server. It is a trimmed port of the cmua search stack: the Anthropic server-side `web_search` tool runs through the `fantasy` agent framework, and `WebFetch`/`GetTime` are the agent's client tools.

- `main.go` — calls `cmd.Execute()`
- `cmd/` — cobra CLI: `root` (with no subcommand it redirects to `server`), `server` (assembles providers, runs the HTTP server, graceful shutdown)
- `internal/service/search/` — domain logic: `Search` (one agent run per request) and `Fetch` (one public page per call)
- `internal/transport/` — registers middleware and routes (`/healthz`, `/v1/search`, `/v1/fetch`, `/mcp`) on the shared echo server
- `internal/provider/` — DI wiring: `SetupEnv → SetupLogger → SetupLLM → SetupSearch → SetupHttp`
- `pkg/agentutil/` — the WebSearch agent (`agents`), its tools (`tools`), and embedded system prompts (`prompt`)
- `pkg/di`, `pkg/env`, `pkg/httperr`, `pkg/slogtrap`, `pkg/util` — reusable infrastructure

Dependencies point inward: `cmd` → `internal/provider` → `internal/service` → `pkg/*`. `internal/transport` depends only on `internal/service` (through the small `transport.Searcher` interface, which also serves as the test seam).

### HTTP Server

The echo instance is owned by `provider.Http` (`internal/provider/http.go`) and built with `echo.NewWithConfig`: the slog logger, `HTTPErrorHandler`, multipart memory limit, and `echo.LegacyIPExtractor`. `StartConfig.BeforeServeFunc` applies `READ_TIMEOUT` / `WRITE_TIMEOUT` / `IDLE_TIMEOUT` / `MAX_HEADER_BYTES` to the `http.Server` and pins `BaseContext` to the app context.

`transport.SetupHttp(ctx)` resolves `provider.Env`, `*search.Service` and `provider.Http` from the container, installs the middleware chain (`Recover → RequestID → RequestLogger`) and calls `register`, which mounts the routes. `cmd.httpRunner` then serves it with `httpServer.StartConfig.Start(ctx, httpServer)`. Tests call the same `useMiddleware` + `register` helpers on a fresh echo instance with the error handler from `pkg/httperr`, so the production chain is covered without a network listener.

Keep `WRITE_TIMEOUT` longer than the slowest realistic search; async/streaming responses are intentionally not supported.

## Dependency Injection

`pkg/di` is a context-based container:

```go
di.Provide(ctx, func(ctx context.Context) (Service, error) { ... })
di.MustInvoke[Service](ctx)
di.Invoke[*Service](ctx)
di.WithCleanup(func(ctx context.Context, s Service) error { ... })
```

Every provider lives in `internal/provider` and is registered by `provider.Setup`. The container keys services by Go type, so `*search.Service` and `*transport.Server` are resolved by pointer type.

## Configuration

`pkg/env.Env` is the authoritative configuration shape; `.env.example` is generated from it by `envdoc` (`go:generate` in `pkg/env/env.go`). Never hand-edit `.env.example`; change `Env` and run `go generate ./...`.

When loaded from a `.env` file the variables carry no prefix; when read from the process environment they require the `APP_` prefix (`provider.EnvConfPrefix`).

## Agent Framework

- The agent is built once per process (`search.New`) and reused; only the prompt changes per request.
- The model is lazily created and cached per model ID in `provider.LLM` (`xsync.Map`), and shared by every request.
- The Anthropic `web_search` tool is a provider-defined server tool: register it with `fantasy.WithProviderDefinedTools(anthropic.WebSearchTool(...))`. It never runs in this process; results come back as `fantasy.SourceContent`, which is how `/v1/search` builds its `sources` list.
- `WEB_SEARCH_MAX_USES` maps to the tool's `max_uses`; `MODEL_OUTPUT_LIMIT` maps to `fantasy.WithMaxOutputTokens`.

### Adding a tool to the search agent

1. Implement `fantasy.AgentTool` in `pkg/agentutil/tools/` (follow `webfetch.go` / `gettime.go`).
2. Register it in `webSearchTools` in `pkg/agentutil/agents/websearch.go`.
3. If it needs configuration, extend `pkg/env.Env`, the `WebSearchConfig`, and `provider.SetupSearch`.
4. Tell the model when to use it in `pkg/agentutil/prompt/websearch_agent_system_prompt.md`.

## HTTP Conventions

- Handlers stay thin: bind input, call the service, map errors. Business logic belongs in `internal/service`.
- Handlers return `*httperr.Error` (`pkg/httperr`) for failures; `httperr.Handler` (wired as the echo `HTTPErrorHandler`) renders the uniform `{"error":{"code","message"}}` envelope, hides internal error text unless `STD_LOG_LEVEL=debug`, turns panics/missing routes into the same shape, and logs every 5xx.
- The service exposes sentinel errors (`search.ErrEmptyQuery`, `ErrInvalidURL`, `ErrFetchFailed`, `ErrFetchDisabled`); the transport maps them to status codes with `errors.Is`.
- `/healthz` is always open; every other endpoint shares the optional bearer-token guard.

## MCP Server

The MCP endpoint is built in `internal/transport/mcp.go` with `github.com/modelcontextprotocol/go-sdk`:

- Tools are added with the generic `sdkmcp.AddTool[In, Out]`; input and output schemas are inferred from struct tags (`json`, `jsonschema`).
- The tool output type is the same `search.Result` the REST API returns, so both surfaces stay in sync.
- The handler returns an error for failed searches, which the SDK packs into a tool error (`isError`) instead of a protocol error.
- Streamable HTTP runs stateless with JSON responses; localhost DNS-rebinding protection is disabled because the default bind address is `0.0.0.0:8231` and the service may sit behind a reverse proxy — access control is the bearer token's job.

## Security Rules for WebFetch

`WebFetch` and `/v1/fetch` must never reach the local network: literal IPs, `localhost`, private, loopback, link-local, CGNAT, multicast, reserved, documentation, benchmark and NAT64 ranges are rejected before the request and again at dial time (DNS rebinding), and every redirect hop is re-validated. Keep `pkg/agentutil/tools/webfetch_test.go` green when touching this code.
