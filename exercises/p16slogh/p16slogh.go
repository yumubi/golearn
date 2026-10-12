// Package p16slogh：P16 的自定义 slog handler 练习，P15 §15.7 改造题的正解。
// CtxHandler：包装任意 handler，从 ctx 自动提取字段（如 req_id）注入，
// 调用点不再每处显式带；MultiHandler：一条记录扇出到多个目的地。
package p16slogh

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"golearn/exercises/p15slog"
)

// CtxHandler 在 Handle 前用 extract 从 ctx 提取 attrs 注入记录。
// extract 是依赖注入而不是硬编码 request id：换 OTel 时注入 trace_id 即可。
type CtxHandler struct {
	inner   slog.Handler
	extract func(context.Context) []slog.Attr
}

// NewCtxHandler 包装任意 handler；extract 返回 nil 表示本条无可注入字段。
func NewCtxHandler(inner slog.Handler, extract func(context.Context) []slog.Attr) slog.Handler {
	return &CtxHandler{inner: inner, extract: extract}
}

// NewCtxLogger 生产便捷封装：JSON 输出 + req_id 自动注入。
func NewCtxLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewCtxHandler(
		slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}),
		func(ctx context.Context) []slog.Attr {
			if id := p15slog.RequestID(ctx); id != "" {
				return []slog.Attr{slog.String("req_id", id)}
			}
			return nil
		}))
}

// Enabled 直接转发：级别判断属于内层 handler 的职责。
func (h *CtxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle 注入发生在记录进入内层之前，JSONHandler 照常序列化。
// 同名字段让位给调用点：标准 JSONHandler 对同名 attr 不去重（两条并存），
// 注入若先排会出现重复 key，读侧取值不可控，所以先查重再注入。
func (h *CtxHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs := h.extract(ctx); len(attrs) > 0 {
		for _, a := range attrs {
			if !recordHas(r, a.Key) {
				r.AddAttrs(a)
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

// recordHas 遍历记录自带的 attrs（不含 With 预置的，那在内层 handler 里）。
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

// WithAttrs/WithGroup 必须转发并保持包装，否则 With 链上的 logger 丢注入。
// 这是自定义 handler 最常见的翻车点，测试 TestWithAttrsKeepsInjection 盯着它。
func (h *CtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CtxHandler{inner: h.inner.WithAttrs(attrs), extract: h.extract}
}

func (h *CtxHandler) WithGroup(name string) slog.Handler {
	return &CtxHandler{inner: h.inner.WithGroup(name), extract: h.extract}
}

// MultiHandler 把一条记录扇出到多个 handler（stdout JSON + 文件文本的常见需求）。
// Enabled 语义是"任一感兴趣就放行"，级别过滤交给各 handler 自己做。
func MultiHandler(handlers ...slog.Handler) slog.Handler { return multiHandler(handlers) }

type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

// Handle 尽力而为：一个 handler 失败不拦其他，错误用 errors.Join 聚合。
func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}
