# DeepSearch

简体中文 · [English](./README.md)

DeepSearch 把 DeepSeek 模型与 Anthropic API 的 Server Tool Use `web_search` 封装成对大语言模型友好的 HTTP API 与 MCP Server：一次查询换回一段答案、答案引用的来源 URL，以及这次搜索消耗的 token 用量。

模型走任意 Anthropic 兼容端点，`web_search` 由服务端执行，片段不够用时再由本地 `WebFetch` 读取单个公开网页正文。

## 快速开始

```bash
go generate ./...        # 由 pkg/env 生成 .env.example
cp .env.example .env     # 至少填写 MODEL_API_KEY / MODEL_BASE_URL / MODEL
go run . server -d .env  # 默认监听 0.0.0.0:8231
```

不传子命令时同样进入 `server`。`MODEL_BASE_URL` 必须是兼容 Anthropic Messages API 且支持服务端 `web_search` 工具的端点，`MODEL` 是它背后的模型 ID，例如 `deepseek-flash`。

## 容器

```bash
docker run -d --name deepsearch -p 8231:8231 -v "$PWD/.env:/app/.env:ro" ghcr.io/clarkqaq/deepsearch:latest
```

或用 Compose（会挂载 `.env` 并继承镜像的健康检查）：

```bash
docker compose pull && docker compose up -d
```

每次 `v*` tag 会把 `linux/amd64` 镜像发布到 `ghcr.io/clarkqaq/deepsearch`，标签为 `<version>`、`<major>.<minor>` 与 `latest`；新建的 GHCR 包默认私有，需要匿名拉取时在仓库设置里改成 public。

镜像以非 root 用户 `deepsearch` 运行，健康检查轮询 `/healthz`。配置来自挂载到 `/app/.env` 的 `.env` 文件，用 `APP_` 前缀的环境变量同样可以。监听端口取自配置里的 `HTTP_ADDR`，因此 `DEEPSEARCH_PORT` 要与它一致 —— 例如 `.env` 写的是 `HTTP_ADDR=0.0.0.0:8232` 时用 `DEEPSEARCH_PORT=8232 docker compose up -d`。从源码构建镜像的方式见 [AGENTS.md](AGENTS.md)。

## 构建与测试

```bash
go build ./...
go vet ./...
go test -race ./...
```

## REST 用法

```bash
curl -s http://127.0.0.1:8231/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"Go 1.27 有哪些变化"}' | jq
```

```json
{
  "answer": "……(纯文本答案，含来源 URL)……",
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
  "text": "……页面正文……",
  "links": [{"url": "https://go.dev/", "text": "The Go Programming Language"}],
  "truncated": false
}
```

错误响应统一为：

```json
{"error": {"code": "invalid_request", "message": "query is required"}}
```

| HTTP | code | 场景 |
| --- | --- | --- |
| 400 | `invalid_request` | 请求体不合法、query 为空、URL 不可读 |
| 401 | `unauthorized` | 配置了 token 但未提供或校验失败 |
| 404 | `fetch_disabled` | `WEB_FETCH_ENABLED=false` 时调用 `/v1/fetch` |
| 502 | `search_failed` / `fetch_failed` | 搜索失败、页面读取失败 |
| 500 | `internal` | 其它内部错误 |

## 能力

| 接口 | 说明 |
| --- | --- |
| `POST /v1/search` | 同步搜索，返回答案、来源、用量、耗时 |
| `POST /v1/fetch` | 读取单个公开网页，返回标题、正文、外链 |
| `GET /healthz` | 健康检查，始终免鉴权 |
| `POST /mcp` | MCP Streamable HTTP，暴露 `web_search` 工具 |

只有同步接口，没有流式与异步任务。

## 配置

完整列表见 [.env.example](.env.example)，由 `go generate ./...` 从 `pkg/env.Env` 生成。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MODEL_API_KEY` | 空 | 模型 API Key，必填 |
| `MODEL_BASE_URL` | 空 | 模型 API 基地址 |
| `MODEL` | 空 | 模型 ID，必填，例如 `deepseek-flash` |
| `MODEL_OUTPUT_LIMIT` | 空 | 单次答案的最大输出 token |
| `WEB_SEARCH_MAX_USES` | `10` | 单次请求内 `web_search` 服务端工具的最大调用次数 |
| `WEB_FETCH_ENABLED` | `true` | 是否启用 `WebFetch`（同时决定 `/v1/fetch` 是否可用） |
| `WEB_FETCH_TIMEOUT` | `15s` | 单次抓取超时（含 DNS、拨号、重定向与读取） |
| `WEB_FETCH_MAX_BYTES` | `2097152` | 单页最多读取的字节数 |
| `WEB_FETCH_MAX_CHARS` | `20000` | 返回给模型的最大字符数 |
| `HTTP_ADDR` | `0.0.0.0:8231` | REST 与 MCP 的监听地址 |
| `AUTH_TOKEN` | 空 | 可选 Bearer Token，REST 与 MCP 共用；配置后除 `/healthz` 外都要求 `Authorization: Bearer <token>` |
| `MAX_HEADER_BYTES` | `2097152` | HTTP 请求头最大字节数 |
| `MAX_MULTIPART_MEMORY` | `67108864` | 解析 multipart 表单时允许占用的最大内存，超出后落盘 |
| `READ_TIMEOUT` | `120s` | 读取完整请求（含 body）的最长耗时 |
| `WRITE_TIMEOUT` | `300s` | 写响应的最长耗时；必须覆盖一次完整搜索 |
| `IDLE_TIMEOUT` | `30s` | 空闲连接保持时长 |
| `STD_LOG_LEVEL` | `info` | 控制台日志级别 |

读取 `.env` 时变量不带前缀；直接使用系统环境变量时需要 `APP_` 前缀（例如 `APP_MODEL_API_KEY`）。

## MCP 接入

MCP 只暴露一个工具 `web_search`，入参 `{"query": "..."}`，返回值与 `/v1/search` 同构（同时作为结构化输出返回）。

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

未配置 `AUTH_TOKEN` 时可省略 `headers`。

## 架构

```
cmd/                       cobra：root（无子命令转 server）、server（装配 provider 并起 HTTP）
internal/provider/         DI 装配：env → logger → llm → search → http
internal/service/search/   搜索域：Search（Agent 单轮运行）、Fetch（单页读取）
internal/transport/        在 provider.Http 上注册 REST 路由与 `/mcp`（MCP Streamable HTTP）
pkg/agentutil/             WebSearch Agent、WebFetch/GetTime 工具、系统提示词
pkg/di, pkg/env, pkg/httperr, pkg/slogtrap, pkg/util
```

依赖方向自外向内：`cmd` → `internal/provider` → `internal/service` → `pkg/*`，`internal/transport` 只依赖 `internal/service`。服务实例由 `pkg/di` 容器按类型装配，`provider.Setup` 负责全部注册。

HTTP 服务由 `internal/provider/http.go` 统一构建（`echo.NewWithConfig` + `StartConfig`：读写超时、最大请求头、multipart 内存上限、`BaseContext`），`internal/transport` 只往这个共享 echo 实例上挂中间件与路由，`cmd` 用 `StartConfig.Start(ctx, http)` 启动；所有 handler 错误经 `pkg/httperr` 渲染成上面的统一信封（`STD_LOG_LEVEL=debug` 时暴露内部错误文本）。

## 安全

`WebFetch`（含 `/v1/fetch`）只允许公开域名与公网地址：字面 IP、`localhost`、内网、CGNAT、链路本地、多播、保留与基准测试网段全部拒绝，重定向每一跳都重新校验，并在 DNS 解析后、真正拨号前再校验一次对端地址，避免 DNS rebinding。

## 许可

基于 MIT 协议发布，完整文本见 [LICENSE](LICENSE)。
