package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
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

// TestMetricsGuarded 守护 Instrument 的两个行为：in_flight 配对回收、
// requests_total 按路由模板记数。对应 P13 台账变异 p13-inflight-dec
// （删 InFlight.Dec 后此测试必须失败）。
func TestMetricsGuarded(t *testing.T) {
	st := newStore()
	mux := http.NewServeMux()
	registerRoutes(mux, st, testLogger())

	// 打 3 个请求（2 PUT + 1 GET miss）
	body, _ := json.Marshal(Item{Value: "v", TTL: 30})
	for range 2 {
		req, _ := http.NewRequest(http.MethodPut, "/kv/k", bytes.NewReader(body))
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/kv/ghost", nil))

	// promhttp 输出到默认 registry；这里直接读 gatherer
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		switch mf.GetName() {
		case "httpkv_in_flight_requests":
			if v := mf.GetMetric()[0].GetGauge().GetValue(); v != 0 {
				t.Fatalf("in_flight = %v, want 0 (Dec leaked)", v)
			}
		case "httpkv_requests_total":
			// ghost GET 必须记 status="404"：statusWriter 截获 WriteHeader 的证据。
			// 变异 p13-statuswriter（WriteHeader 强制 200）应在此失败。
			found404 := false
			for _, m := range mf.GetMetric() {
				status := ""
				for _, lp := range m.GetLabel() {
					if lp.GetName() == "status" {
						status = lp.GetValue()
					}
					if lp.GetName() == "path" && !strings.Contains(lp.GetValue(), "/kv/{key}") {
						t.Fatalf("path label = %q, want route template", lp.GetValue())
					}
				}
				if status == "404" {
					found404 = true
				}
			}
			if !found404 {
				t.Fatal("no status=404 series: WriteHeader not captured")
			}
		}
	}
}
