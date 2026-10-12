package p19eco

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestFunctionalOptions 改造题答案：默认值 + options 覆盖。
func TestFunctionalOptions(t *testing.T) {
	// 不传 options：默认值生效
	s := NewServer(":8080")
	if s.Addr() != ":8080" || s.Timeout() != 5*time.Second {
		t.Fatalf("默认值错误: %s %s", s.Addr(), s.Timeout())
	}
	// 传 options：覆盖默认
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	s2 := NewServer(":9090", WithTimeout(10*time.Second), WithLogger(logger))
	if s2.Timeout() != 10*time.Second {
		t.Fatalf("WithTimeout 未生效: %s", s2.Timeout())
	}
	if s2.Logger() != logger {
		t.Fatal("WithLogger 未生效")
	}
}

// TestSlogHandler 改造题：自定义 Handler 的 Enabled 门控——
// 生态库（zap/OTel/loki handler）全部长在 slog.Handler 接口上。
type warnOnlyHandler struct{ buf *bytes.Buffer }

func (h warnOnlyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn // 改造点：只放行 warn 及以上
}

func (h warnOnlyHandler) Handle(_ context.Context, r slog.Record) error {
	h.buf.WriteString(r.Message + "\n")
	return nil
}

func (h warnOnlyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h warnOnlyHandler) WithGroup(_ string) slog.Handler      { return h }

func TestSlogHandler(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(warnOnlyHandler{buf: buf})

	logger.Info("this should be filtered")  // Enabled=false，不落
	logger.Warn("this should pass")          // 落
	logger.Error("this should pass too")     // 落

	out := buf.String()
	if strings.Contains(out, "filtered") {
		t.Fatalf("Info 应被 Enabled 门控掉，got %q", out)
	}
	if !strings.Contains(out, "pass") || !strings.Contains(out, "pass too") {
		t.Fatalf("Warn/Error 应通过，got %q", out)
	}
}

// TestStringer 观察题：go generate 后 Level.String() 可用（stringer 产物）。
func TestStringer(t *testing.T) {
	if got := Warn.String(); got != "warn" { // -linecomment 生成的名字
		t.Fatalf("Warn.String()=%q, want warn（先跑 go generate ./exercises/p19eco/）", got)
	}
	if got := Error.String(); got != "error" {
		t.Fatalf("Error.String()=%q, want error", got)
	}
}

// TestPingCtx 观察题：ctx 取消优先于业务逻辑——生态契合度的行为证据。
func TestPingCtx(t *testing.T) {
	s := NewServer(":0", WithTimeout(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	if err := s.Ping(ctx); err == nil {
		t.Fatal("已取消的 ctx 应让 Ping 返回错误")
	}
}
