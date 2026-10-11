package p15slog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// newTestLogger：JSON 写进 buffer，测试可断言字段。
func newTestLogger(level slog.Level) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})
	return slog.New(h), &buf
}

// TestJSONFields：一条日志 = 一个 JSON 对象，字段可查询。
func TestJSONFields(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	logger.Info("request done", "method", "GET", "status", 200)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if rec["msg"] != "request done" || rec["method"] != "GET" || rec["status"] != float64(200) {
		t.Fatalf("fields = %v", rec)
	}
	if rec["level"] != "INFO" {
		t.Fatalf("level = %v", rec["level"])
	}
}

// TestLevelFilter：级别过滤，Debug 不落盘。
func TestLevelFilter(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	logger.Debug("should not appear")
	if buf.Len() != 0 {
		t.Fatalf("debug leaked: %q", buf.String())
	}
	logger.Warn("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Fatalf("warn missing: %q", buf.String())
	}
}

// TestLogValuerRedact：LogValuer 脱敏——Token 不出现，Email 打码。
func TestLogValuerRedact(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	logger.Info("login", "user", User{ID: 7, Email: "alice@example.com", Token: "sekrit"})

	out := buf.String()
	if strings.Contains(out, "sekrit") || strings.Contains(out, "alice@") {
		t.Fatalf("sensitive data leaked: %s", out)
	}
	if !strings.Contains(out, "a***@example.com") {
		t.Fatalf("redaction wrong: %s", out)
	}
}

// TestCtxRequestID：中间件塞 id，LogAttrs/InfoContext 带出来。
func TestCtxRequestID(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	ctx := WithRequestID(context.Background(), "req-42")

	// handler 层面用 attrs 显式带；生产里用自定义 handler 从 ctx 自动注入
	logger.InfoContext(ctx, "handled", "req_id", RequestID(ctx))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "req-42" {
		t.Fatalf("req_id = %v", rec["req_id"])
	}
}

// TestLogKV：结构体转 Attrs，一条日志一个事件。
func TestLogKV(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	LogKV(WithRequestID(context.Background(), "req-7"), logger, "kv miss", KV{
		Method: "GET", Path: "/kv/foo", Status: 404,
	})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["method"] != "GET" || rec["status"] != float64(404) || rec["req_id"] != "req-7" {
		t.Fatalf("rec = %v", rec)
	}
}

// TestEnabledGuard：昂贵计算被级别守卫拦住（EnabledDemo 里 expensive 不该被调）。
func TestEnabledGuard(t *testing.T) {
	logger, buf := newTestLogger(slog.LevelInfo)
	called := false
	EnabledDemo(logger, func() string {
		called = true
		return "heavy"
	})
	if called {
		t.Fatal("expensive func ran despite level guard")
	}
	if buf.Len() != 0 {
		t.Fatalf("debug output leaked at info level: %q", buf.String())
	}
}
