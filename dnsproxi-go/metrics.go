// Package main 的指标定义：dnsproxi 的 RED + 缓存效能。
// label 纪律（P13 §13.7）：qtype 映射到有限集合（其余归 OTHER），
// outcome 是有限枚举；qname 高基数，绝不进 label（要查名字用日志）。
package main

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// QueriesTotal：请求量（RED 的 R）。outcome 枚举：hit|miss|sinkhole|short|upstream_error。
	QueriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsproxi_queries_total",
		Help: "DNS queries by qtype and outcome.",
	}, []string{"qtype", "outcome"})

	// UpstreamErrorsTotal：上游错误按原因分类（P17 §17.6 改造题落地）。
	// reason 枚举：timeout|network|refused。告警规则按 reason 分通道：
	// timeout 涨 = 上游过载/黑洞；refused 涨 = 上游端口没开。
	UpstreamErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsproxi_upstream_errors_total",
		Help: "Upstream failures by reason (timeout, network, refused).",
	}, []string{"reason"})

	UpstreamLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "dnsproxi_upstream_duration_seconds",
		Help:    "Upstream round-trip latency for forwarded queries.",
		Buckets: []float64{0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.05, 0.1, 0.5},
	})

	// InFlight：在飞查询数（worker pool 满时读循环背压，这个数顶到 WorkerPool）。
	InFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dnsproxi_in_flight_queries",
		Help: "Queries currently being handled.",
	})

	// CacheEntries：缓存条目数（容量规划看它逼近 CacheMax 没）。
	CacheEntries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dnsproxi_cache_entries",
		Help: "Current number of cached responses.",
	})

	// MetricsHandler：Prometheus 抓取口。GET 才有意义，caller 自己接 mux。
	MetricsHandler = promhttp.Handler
)

// upstreamErrReason 把上游读/写错误分类成低基数 reason。
// net.Error.Timeout() 是接口断言：UDP 读超时是 *net.OpError 包着
// os.ErrDeadlineExceeded。ECONNREFUSED 是对端 ICMP 端口不可达的浮出形态。
// 兜底归 network：分类器自己不能 panic，未知原因也要能数出来。
func upstreamErrReason(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	return "network"
}

// qtypeLabel 把 16 位 qtype 映射到低基数 label：常见类型实名，其余 OTHER。
// 域名里 A=1 AAAA=28 CNAME=5 NS=2 MX=15 TXT=16 PTR=12 SRV=33 HTTPS=65。
func qtypeLabel(t uint16) string {
	switch t {
	case 1:
		return "A"
	case 28:
		return "AAAA"
	case 5:
		return "CNAME"
	case 2:
		return "NS"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 12:
		return "PTR"
	case 33:
		return "SRV"
	case 65:
		return "HTTPS"
	default:
		return "OTHER"
	}
}

// metricsServer 在独立端口起 HTTP 抓取口（对照 httpkv 的做法：
// 生产指标口不能和业务端口混在一个面上，避免被公网扫到）。
func metricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", MetricsHandler())
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
