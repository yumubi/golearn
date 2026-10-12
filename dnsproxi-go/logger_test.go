package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestLoggerInjectsReqID：handleQuery 语境里 req_id 经 ctx 自动注入。
func TestLoggerInjectsReqID(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	ctx := withRequestID(context.Background(), "q-test-1")

	logger.InfoContext(ctx, "cache miss, forwarding", "qname", "a.test")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "q-test-1" {
		t.Fatalf("req_id = %v, want injected from ctx", rec["req_id"])
	}
}

// TestLoggerExplicitBeatsInjected：调用点显式带 req_id 时输出唯一（查重生效）。
func TestLoggerExplicitBeatsInjected(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	ctx := withRequestID(context.Background(), "q-ctx")

	logger.InfoContext(ctx, "both", "req_id", "q-explicit")

	out := buf.String()
	if strings.Count(out, `"req_id"`) != 1 {
		t.Fatalf("req_id duplicated: %s", out)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "q-explicit" {
		t.Fatalf("req_id = %v, want explicit", rec["req_id"])
	}
}

// TestLoggerWithAttrsKeepsInjection：With 链后注入不丢（坑 1）。
func TestLoggerWithAttrsKeepsInjection(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo).With("svc", "dnsproxi")
	ctx := withRequestID(context.Background(), "q-with")

	logger.InfoContext(ctx, "via-with")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "q-with" || rec["svc"] != "dnsproxi" {
		t.Fatalf("got %v (svc/req_id must survive With chain)", rec)
	}
}

// TestLogLevelFallback：DNS_LOG_LEVEL 非法回 info。
func TestLogLevelFallback(t *testing.T) {
	t.Setenv("DNS_LOG_LEVEL", "nonsense")
	if got := logLevel(); got != slog.LevelInfo {
		t.Fatalf("logLevel = %v, want info fallback", got)
	}
	t.Setenv("DNS_LOG_LEVEL", "warn")
	if got := logLevel(); got != slog.LevelWarn {
		t.Fatalf("logLevel = %v, want warn", got)
	}
}
