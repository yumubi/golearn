package main

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// gatherCounter 从默认 registry 读一条 counter 序列值（按 label 匹配）。
func gatherCounter(t *testing.T, name string, wantLabels map[string]string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			match := len(labels) == len(wantLabels)
			for k, v := range wantLabels {
				if labels[k] != v {
					match = false
				}
			}
			if match {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0 // 序列未出现 = 计数为 0
}

// gatherHistCount 读直方图样本总数。
func gatherHistCount(t *testing.T, name string) uint64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			return m.GetHistogram().GetSampleCount()
		}
	}
	return 0
}

// encodeFakeReply 拼一个最小 DNS 应答（QR=1，1 条 A 记录 10.0.0.1）。
func encodeFakeReply(query []byte) []byte {
	resp := append([]byte(nil), query...)
	resp[2] |= 0x80 // QR=1
	resp = append(resp, 0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01)
	resp = binary.BigEndian.AppendUint32(resp, 60)
	resp = append(resp, 0x00, 0x04, 10, 0, 0, 1)
	return resp
}

// TestMetricsDeadUpstream：死上游 3 查 → refused 3、outcome=upstream_error 3、
// 直方图 0 样本（错误样本不进时延直方图）。守护 P17 改造题表格第一行
// 与变异 p17-err-histogram（把错误样本塞进直方图必须失败）。
func TestMetricsDeadUpstream(t *testing.T) {
	dead, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.LocalAddr().String()
	dead.Close() // 端口立刻没人听

	proxyAddr := startProxy(t, Config{Listen: "127.0.0.1:0", Upstream: deadAddr})
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 同进程其它测试（如 TestD1Timeout 黑洞）会先动全局 registry，
	// 所以断言用"打查询前的基线 + 增量"而非绝对值。UDP 错误浮出有延迟
	// （上一查询的 read refused 可能在基线后才落账），先等账面稳定。
	time.Sleep(500 * time.Millisecond)
	baseErr := gatherCounter(t, "dnsproxi_upstream_errors_total", map[string]string{"reason": "refused"})
	baseQE := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "upstream_error", "qtype": "A"})
	baseHist := gatherHistCount(t, "dnsproxi_upstream_duration_seconds")

	for i := range 3 {
		if _, err := client.WriteToUDP(dnsQuery(uint16(100+i), "dead.test"), proxyAddr); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)

	if got := gatherCounter(t, "dnsproxi_upstream_errors_total", map[string]string{"reason": "refused"}); got-baseErr != 3 {
		t.Fatalf("upstream_errors_total{refused} delta = %v, want 3", got-baseErr)
	}
	if got := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "upstream_error", "qtype": "A"}); got-baseQE != 3 {
		t.Fatalf("queries_total{upstream_error} delta = %v, want 3", got-baseQE)
	}
	if n := gatherHistCount(t, "dnsproxi_upstream_duration_seconds"); n != baseHist {
		t.Fatalf("histogram samples = %d, want unchanged %d (errors must not be timed)", n, baseHist)
	}
}

// TestMetricsSinkhole：sinkhole 命中不计上游、outcome=sinkhole。
// 守护变异 p17-qtype-other（qtypeLabel 全 OTHER 必须失败：sinkhole 序列 qtype 变了）。
func TestMetricsSinkhole(t *testing.T) {
	proxyAddr := startProxy(t, Config{Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", Sinkhole: "blocked."})
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	baseSink := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "sinkhole", "qtype": "A"})

	if _, err := client.WriteToUDP(dnsQuery(200, "blocked.test"), proxyAddr); err != nil {
		t.Fatal(err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	if _, _, err := client.ReadFromUDP(buf); err != nil {
		t.Fatal("sinkhole must reply NXDOMAIN")
	}
	time.Sleep(100 * time.Millisecond)

	if got := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "sinkhole", "qtype": "A"}); got-baseSink < 1 {
		t.Fatalf("queries_total{sinkhole,qtype=A} delta = %v, want >= 1", got-baseSink)
	}
}

// TestMetricsForwardCounts：正常转发 2 查 → forwarded 2、直方图 2 样本。
// 守护 Take-away 1（成功路径才量时延）与 p17-forwarded-metric 变异。
func TestMetricsForwardCounts(t *testing.T) {
	up, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := up.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = up.WriteToUDP(encodeFakeReply(buf[:n]), addr)
		}
	}()

	proxyAddr := startProxy(t, Config{Listen: "127.0.0.1:0", Upstream: up.LocalAddr().String()})
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 全局 registry 被同进程其它测试共享，断言用增量。
	baseFwd := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "forwarded", "qtype": "A"})
	baseHist := gatherHistCount(t, "dnsproxi_upstream_duration_seconds")

	for i := range 2 {
		if _, err := client.WriteToUDP(dnsQuery(uint16(300+i), "fwd.test"), proxyAddr); err != nil {
			t.Fatal(err)
		}
		client.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 1500)
		if _, _, err := client.ReadFromUDP(buf); err != nil {
			t.Fatal("no reply from proxy")
		}
	}
	time.Sleep(100 * time.Millisecond)

	if got := gatherCounter(t, "dnsproxi_queries_total", map[string]string{"outcome": "forwarded", "qtype": "A"}); got-baseFwd != 2 {
		t.Fatalf("queries_total{forwarded} delta = %v, want 2", got-baseFwd)
	}
	if n := gatherHistCount(t, "dnsproxi_upstream_duration_seconds"); n-baseHist != 2 {
		t.Fatalf("histogram samples delta = %d, want 2", n-baseHist)
	}
}
