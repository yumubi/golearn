package p16slogh

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"golearn/exercises/p15slog"
)

// TestAutoInjection：ctx 里有 req_id，调用点不带任何字段，JSON 里也有。
func TestAutoInjection(t *testing.T) {
	var buf bytes.Buffer
	logger := NewCtxLogger(&buf, slog.LevelInfo)
	ctx := p15slog.WithRequestID(context.Background(), "req-99")

	logger.InfoContext(ctx, "handled") // 注意：调用点零显式字段

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "req-99" {
		t.Fatalf("req_id = %v, want auto-injected req-99", rec["req_id"])
	}
}

// TestNoCtxNoField：ctx 里没有 req_id 就不出现空字段，输出保持干净。
func TestNoCtxNoField(t *testing.T) {
	var buf bytes.Buffer
	logger := NewCtxLogger(&buf, slog.LevelInfo)

	logger.Info("no ctx here")

	if strings.Contains(buf.String(), "req_id") {
		t.Fatalf("req_id should be absent: %s", buf.String())
	}
}

// TestWithAttrsKeepsInjection：With 链之后注入仍然生效（WithAttrs 转发的关键）。
func TestWithAttrsKeepsInjection(t *testing.T) {
	var buf bytes.Buffer
	logger := NewCtxLogger(&buf, slog.LevelInfo).With("service", "httpkv")
	ctx := p15slog.WithRequestID(context.Background(), "req-7")

	logger.InfoContext(ctx, "with chain")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["service"] != "httpkv" || rec["req_id"] != "req-7" {
		t.Fatalf("rec = %v, want service+req_id both present", rec)
	}
}

// TestLevelForwarding：注入包装不该破坏级别过滤。
func TestLevelForwarding(t *testing.T) {
	var buf bytes.Buffer
	logger := NewCtxLogger(&buf, slog.LevelWarn)
	ctx := p15slog.WithRequestID(context.Background(), "req-1")

	logger.DebugContext(ctx, "filtered")
	if buf.Len() != 0 {
		t.Fatalf("debug leaked: %q", buf.String())
	}
	logger.WarnContext(ctx, "kept")
	if !strings.Contains(buf.String(), `"req_id":"req-1"`) {
		t.Fatalf("warn lost req_id: %s", buf.String())
	}
}

// TestMultiHandler：一条记录同时落到两个 handler，各自格式。
func TestMultiHandler(t *testing.T) {
	var jsonBuf, textBuf bytes.Buffer
	h := MultiHandler(
		slog.NewJSONHandler(&jsonBuf, nil),
		slog.NewTextHandler(&textBuf, nil),
	)
	slog.New(h).Info("fanout", "k", "v")

	if !strings.Contains(jsonBuf.String(), `"msg":"fanout"`) {
		t.Fatalf("json sink missing: %s", jsonBuf.String())
	}
	if !strings.Contains(textBuf.String(), "msg=fanout") {
		t.Fatalf("text sink missing: %s", textBuf.String())
	}
}

// TestMultiHandlerLevel：一个 sink 是 Debug 级、另一个 Info 级，各自过滤。
func TestMultiHandlerLevel(t *testing.T) {
	var debugBuf, infoBuf bytes.Buffer
	h := MultiHandler(
		slog.NewJSONHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewJSONHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	logger := slog.New(h)

	logger.Debug("only debug sink sees me")
	if !strings.Contains(debugBuf.String(), "only debug") || infoBuf.Len() != 0 {
		t.Fatalf("debug routing wrong: debug=%q info=%q", debugBuf.String(), infoBuf.String())
	}

	logger.Error("both see me")
	if !strings.Contains(debugBuf.String(), "both see") || !strings.Contains(infoBuf.String(), "both see") {
		t.Fatalf("error routing wrong: debug=%q info=%q", debugBuf.String(), infoBuf.String())
	}
}

// TestExplicitBeatsInjected：调用点显式字段优先，注入的同名字段被调用点覆盖。
// JSONHandler 后加的 attr 覆盖先加的，所以注入放前面（调用点说了算）。
func TestExplicitBeatsInjected(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewCtxHandler(slog.NewJSONHandler(&buf, nil),
		func(ctx context.Context) []slog.Attr {
			return []slog.Attr{slog.String("req_id", "injected")}
		}))
	ctx := p15slog.WithRequestID(context.Background(), "req-x")

	logger.InfoContext(ctx, "both", "req_id", "explicit")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "explicit" {
		t.Fatalf("req_id = %v, want explicit to win", rec["req_id"])
	}
}

// 编译期接口自检。
var (
	_ slog.Handler = (*CtxHandler)(nil)
	_ slog.Handler = multiHandler(nil)
)
