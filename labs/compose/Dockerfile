# 多阶段构建：builder 全家桶不进最终镜像
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY exercises/p15slog/ ./exercises/p15slog/
COPY exercises/p16slogh/ ./exercises/p16slogh/
COPY exercises/p8fast/httpkv/ ./exercises/p8fast/httpkv/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/httpkv ./exercises/p8fast/httpkv/

FROM alpine:3.22
COPY --from=build /out/httpkv /usr/local/bin/httpkv
USER nobody
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/httpkv"]
