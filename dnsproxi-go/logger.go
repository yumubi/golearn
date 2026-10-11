// logger.go：dnsproxi 的结构化日志。P16 手艺在毕业项目里的重写：
// JSONHandler + ctx 自动注入 req_id，调用点只写业务字段。
// 不 import golearn 练习包：dnsproxi 是独立 module，自包含。
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// reqIDKey ctx 键。每查询一个 id（UDP 没有 HTTP 中间件，读循环生成）。
type reqIDKey struct{}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, reqIDKey{}, id)
}

func requestID(ctx context.Context) string {
	if id, ok := ctx.Value(reqIDKey{}).(string); ok {
		return id
	}
	return ""
}

// newQueryID 生成请求 id。练习版 UnixNano 够用（同机冒烟可读）；
// 生产跨实例要 crypto/rand 或 ulid（P15 §15.6 同款取舍）。
func newQueryID() string {
	return fmt.Sprintf("q-%d", time.Now().UnixNano())
}

// ctxHandler 包装 JSONHandler：Handle 前从 ctx 提取 req_id 注入。
// 两个坑（P16 实测）：WithAttrs/WithGroup 必须转发重包；同名 attr
// 按首次出现定型，注入前查重让调用点的显式字段赢。
type ctxHandler struct {
	inner slog.Handler
}

func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(&ctxHandler{
		inner: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}),
	})
}

func (h *ctxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestID(ctx); id != "" {
		if !recordHas(r, "req_id") {
			r.AddAttrs(slog.String("req_id", id))
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *ctxHandler) WithGroup(name string) slog.Handler {
	return &ctxHandler{inner: h.inner.WithGroup(name)}
}

// recordHas 遍历记录自带 attrs（With 预置的在内层 handler 里）。
func recordHas(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}

// logger 包级默认：stderr JSON。main() 里按 DNS_LOG_LEVEL 重置级别。
var logger = newLogger(os.Stderr, slog.LevelInfo)

// logLevel 解析 DNS_LOG_LEVEL（debug|info|warn|error），非法回 info。
func logLevel() slog.Level {
	switch os.Getenv("DNS_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
