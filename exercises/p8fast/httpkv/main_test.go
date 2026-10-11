package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testLogger 静音日志输出，测试只断言响应不assert日志。
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// 端到端测试：httptest 起真实 mux（不占端口），打四类请求。
// 对照 Rust: axum-test / reqwest + test server。
func TestHTTPKV(t *testing.T) {
	st := newStore()
	mux := http.NewServeMux()
	registerRoutes(mux, st, testLogger()) // P15 起带 logger；测试用静音 handler

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()
	// PUT
	body, _ := json.Marshal(Item{Value: "hello", TTL: 30})
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/kv/greet", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", resp.StatusCode)
	}

	// GET 命中
	resp, err = client.Get(ts.URL + "/kv/greet")
	if err != nil {
		t.Fatal(err)
	}
	var got Item
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Value != "hello" || got.TTL != 30 {
		t.Fatalf("GET = %+v", got)
	}
	if got.At.IsZero() {
		t.Fatal("At should be auto-filled")
	}

	// GET 未命中
	resp, _ = client.Get(ts.URL + "/kv/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing key status = %d, want 404", resp.StatusCode)
	}

	// PUT 未知字段被拒（DisallowUnknownFields）
	badBody := `{"value":"x","bogus":1}`
	bad, err := http.NewRequest(http.MethodPut, ts.URL+"/kv/bad", strings.NewReader(badBody))
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", resp2.StatusCode)
	}

	// DELETE
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/kv/greet", nil)
	resp3, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp3.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp3.StatusCode)
	}
	resp4, _ := client.Get(ts.URL + "/kv/greet")
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatal("deleted key still readable")
	}
}

// TestPathValue 单测 1.22+ 路由参数。
func TestPathValue(t *testing.T) {
	st := newStore()
	st.put("k1", Item{Value: "v1"})
	mux := http.NewServeMux()
	registerRoutes(mux, st, testLogger())

	req := httptest.NewRequest("GET", "/kv/k1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var it Item
	_ = json.Unmarshal(rec.Body.Bytes(), &it)
	if it.Value != "v1" {
		t.Fatalf("got %+v", it)
	}
	fmt.Println("status:", rec.Code)
}
