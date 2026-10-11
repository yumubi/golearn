// Package p15slog：P15 的 slog 结构化日志练习。
// 覆盖：JSON handler、级别控制、LogValuer 脱敏、ctx 关联（P13 §13.8 路线图第一步）。
// 零环境依赖，-race 可测。
package p15slog

import (
	"context"
	"log/slog"
	"os"
)

// RequestIDKey ctx 里传 request id 的键。中间件塞、日志取。
type ctxKey string

const RequestIDKey ctxKey = "request_id"

// NewLogger 生产形状的 logger：JSON 到 stdout，级别可调。
// 对照 Rust tracing_subscriber::fmt().json()。
func NewLogger(level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// WithRequestID 中间件把 request id 塞进 ctx（对照 tower 的 Extension layer）。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, RequestIDKey, id)
}

// RequestID 从 ctx 取 request id，没有返回空串。
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey).(string); ok {
		return v
	}
	return ""
}

// User 演示 LogValuer：敏感字段只在日志输出时脱敏。
// LogValuer 是 slog 的延迟求值接口：Log() 时才调用 LogValue()。
type User struct {
	ID    int64
	Email string
	Token string
}

// LogValue 把 Token 收掉、Email 只留域名前一段。
// 对照 tracing 的 field 格式化/RedactPolicy。
func (u User) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("id", u.ID),
		slog.String("email", redactEmail(u.Email)),
	)
}

func redactEmail(e string) string {
	for i := 0; i < len(e); i++ {
		if e[i] == '@' {
			return e[:1] + "***" + e[i:]
		}
	}
	return "***"
}

// KV 结构化日志的价值演示：一条日志 = 一个可查询的事件。
type KV struct {
	Method string
	Path   string
	Status int
}

// Attrs 把结构体转成 slog 字段（统一命名：snake_case，跨服务一致）。
func (kv KV) Attrs() []slog.Attr {
	return []slog.Attr{
		slog.String("method", kv.Method),
		slog.String("path", kv.Path),
		slog.Int("status", kv.Status),
	}
}

// LogKV 把 ctx 里的 request id 和结构体字段带出来。
// InfoContext vs Info：带 ctx 的版本让 handler 能读到 span 等上下文值；
// 标准 handler 不自动读 ctx，所以 req_id 在这里显式取出。
func LogKV(ctx context.Context, logger *slog.Logger, msg string, kv KV) {
	attrs := kv.Attrs()
	if id := RequestID(ctx); id != "" {
		attrs = append(attrs, slog.String("req_id", id))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
}

// EnabledDemo 昂贵操作先查级别再计算（slog 的性能纪律）。
func EnabledDemo(logger *slog.Logger, expensive func() string) {
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		logger.Debug("expensive debug data", "data", expensive())
	}
}
