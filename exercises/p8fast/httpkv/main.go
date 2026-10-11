// Package httpkv：内存 KV 服务，P8 的 net/http/json 考点载体 + P13 的 metrics 演示台。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // /debug/pprof 挂到 DefaultServeMux
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golearn/exercises/p15slog"
	"golearn/exercises/p16slogh"
)

// Item 的 TTL 为 0 表示不过期，omitempty 让它从 JSON 里消失。
type Item struct {
	Value string    `json:"value"`
	At    time.Time `json:"at"`
	TTL   int       `json:"ttl,omitempty"` // 0 → JSON 里省略
}

// store 并发安全的内存 KV（P6 cache 的迷你版）。
type store struct {
	mu sync.RWMutex
	m  map[string]Item
}

func newStore() *store { return &store{m: map[string]Item{}} }

var (
	errNotFound = errors.New("key not found")
)

func (s *store) get(k string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[k]
	if !ok {
		return Item{}, errNotFound
	}
	return v, nil
}

func (s *store) put(k string, v Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.At.IsZero() {
		v.At = time.Now()
	}
	s.m[k] = v
	KeysTotal.Set(float64(len(s.m)))
}

func (s *store) delete(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[k]
	delete(s.m, k)
	if ok {
		KeysTotal.Set(float64(len(s.m)))
	}
	return ok
}
func registerRoutes(mux *http.ServeMux, st *store, logger *slog.Logger) {
	// P15：结构化日志。reqID 中间件最内层（每个 handler 都能取到 id），
	// Instrument 在外层（指标不认 request id，label 基数纪律）。
	mux.Handle("GET /metrics", MetricsHandler())

	mux.Handle("GET /kv/{key}", Instrument("GET /kv/{key}", reqID(func(w http.ResponseWriter, r *http.Request) {
		item, err := st.get(r.PathValue("key"))
		if err != nil {
			// P16 起 CtxHandler 自动注入 req_id，调用点只写业务字段。
			logger.WarnContext(r.Context(), "kv miss", "key", r.PathValue("key"), "status", 404)
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	})))

	mux.Handle("PUT /kv/{key}", Instrument("PUT /kv/{key}", reqID(func(w http.ResponseWriter, r *http.Request) {
		var in Item
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields() // encoding/json 默认忽略未知字段，这里收紧成报错
		if err := dec.Decode(&in); err != nil {
			logger.WarnContext(r.Context(), "bad json", "err", err.Error())
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		st.put(r.PathValue("key"), in)
		logger.InfoContext(r.Context(), "kv put", "key", r.PathValue("key"))
		writeJSON(w, http.StatusCreated, in)
	})))

	mux.Handle("DELETE /kv/{key}", Instrument("DELETE /kv/{key}", reqID(func(w http.ResponseWriter, r *http.Request) {
		if !st.delete(r.PathValue("key")) {
			writeErr(w, http.StatusNotFound, errNotFound.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	mux.Handle("GET /kv", Instrument("GET /kv", reqID(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.keys())
	})))
}

func (s *store) keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	return out
}

// reqID 给每个请求分配 request id，塞进 ctx 并回写响应头。
// ctx 键统一用 p15slog 的（P16 起 CtxLogger 靠它自动注入 req_id）。
func reqID(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fmt.Sprintf("req-%d", time.Now().UnixNano())
		w.Header().Set("X-Request-ID", id)
		next(w, r.WithContext(p15slog.WithRequestID(r.Context(), id)))
	})
}

// requestID 兼容别名：handler 里如需显式取值仍可用。
func requestID(ctx context.Context) string {
	return p15slog.RequestID(ctx)
}

// 默认 logger：writeJSON 这类工具函数没有 logger 参数，落回包级默认。
// P16：CtxLogger = JSONHandler + ctx 自动注入 req_id。
var logger = p16slogh.NewCtxLogger(os.Stderr, slog.LevelInfo)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("encode response", "err", err.Error())
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func main() {
	st := newStore()
	logger = p16slogh.NewCtxLogger(os.Stdout, slog.LevelInfo)
	mux := http.NewServeMux()
	registerRoutes(mux, st, logger)

	// pprof 单独端口；生产不能对公网暴露。
	go http.ListenAndServe("localhost:6060", nil)

	srv := &http.Server{
		Addr:              ":8090",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // slowloris：不设超时连接能无限挂着
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 优雅关停：收到 SIGINT/SIGTERM 后给在飞请求 10s 收尾。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("httpkv on :8090 (pprof on :6060/debug/pprof)")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen failed", "err", err.Error())
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "err", err.Error())
	}
}
