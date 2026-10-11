// 指标定义与请求中间件。同包拆文件，只为让指标定义有单一出处。
// pattern 必须由调用方传：r.Pattern 在 ServeMux 外层恒空（见 Instrument）。
package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// label 全取有限集合：路由模板而非原始 URL——原始 URL 每值一条序列且永不回收，基数爆炸。
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "httpkv_requests_total",
		Help: "Total HTTP requests by method, path template and status code.",
	}, []string{"method", "path", "status"})

	InFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "httpkv_in_flight_requests",
		Help: "Current number of in-flight HTTP requests.",
	})

	// 桶按 KV 的 SLA 定：操作毫秒级。
	Duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "httpkv_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.05, 0.1},
	}, []string{"method", "path"})

	KeysTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "httpkv_keys",
		Help: "Current number of keys in the store.",
	})
)

// Instrument 计数 + 在飞 + 时延三合一。pattern 必须传路由模板字面量：
// r.Pattern 由 ServeMux 匹配后写入，包在 mux 外层时恒为空（实测踩过）。
func Instrument(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		InFlight.Inc()
		defer InFlight.Dec()

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		RequestsTotal.WithLabelValues(r.Method, pattern, strconv.Itoa(sw.status)).Inc()
		Duration.WithLabelValues(r.Method, pattern).Observe(time.Since(start).Seconds())
	})
}

// statusWriter 覆写 WriteHeader 记下状态码；未显式调用时按 200 算。
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// MetricsHandler 用默认 registry：Go runtime 指标（GC/goroutine/fd）随注册表白送。
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
