# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG BUILD_VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w -X deepsearch/pkg/util.version=${BUILD_VERSION}" \
        -o /out/deepsearch .

FROM alpine:3.24 AS runner

ENV TZ=Asia/Shanghai

RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && addgroup -S deepsearch \
    && adduser -S -G deepsearch -h /app deepsearch

WORKDIR /app

LABEL org.opencontainers.image.title="deepsearch" \
      org.opencontainers.image.description="DeepSearch wraps a DeepSeek model and Anthropic's Server Tool Use web_search into an LLM-friendly HTTP API and MCP server" \
      org.opencontainers.image.source="https://github.com/ClarkQAQ/deepsearch"

COPY --from=build /out/deepsearch /usr/local/bin/deepsearch
COPY --chmod=755 scripts/healthcheck.sh /usr/local/bin/healthcheck

RUN mkdir -p /usr/share/doc/deepsearch
COPY --chmod=644 LICENSE /usr/share/doc/deepsearch/LICENSE

USER deepsearch

EXPOSE 8231

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["healthcheck"]

ENTRYPOINT ["/usr/local/bin/deepsearch"]
CMD ["server", "-d", "/app/.env"]
