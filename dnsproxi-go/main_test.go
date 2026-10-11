package main

import (
	"context"
	"net"
	"testing"
	"time"

	"golearn/dnsproxi-go/wire"
)

// fakeUpstream 起一个本地 UDP "DNS 服务器"：收到查询回一个固定应答。
// 对照你 Rust 版 lessonD1-dns/dns-upstream。
func fakeUpstream(t *testing.T) (addr *net.UDPAddr, seen chan []byte) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { pc.Close() })
	seen = make(chan []byte, 16)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			q := append([]byte{}, buf[:n]...)
			select {
			case seen <- q:
			default:
			}
			// 构造应答：QR=1, RCode=0，echo ID 和 question。
			h, _ := wire.ParseHeader(q)
			h.QR = true
			resp := wire.EncodeHeader(h)
			resp = append(resp, q[12:]...) // question 原样带回
			pc.WriteToUDP(resp, from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr), seen
}

// startProxy 起真实 Run 循环并暴露实际端口。
// 做法：freePort 预占一个端口（探明空闲即关），Run 在同端口重新 ListenUDP。
// TOCTOU 窗口存在，但测试环境可接受；G2 若要彻底消除就让 Run 回报实际地址。
func startProxy(t *testing.T, cfg Config) *net.UDPAddr {
	t.Helper()
	port := freePort(t)
	cfg.Listen = net.JoinHostPort("127.0.0.1", itoa(port))
	p := &Proxy{cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // 等 ListenUDP 就位
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
}

func freePort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("freeport: %v", err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func dnsQuery(id uint16, name string) []byte {
	q := wire.EncodeHeader(wire.Header{ID: id, RD: true, QDCount: 1})
	q = append(q, wire.EncodeName(name)...)
	return append(q, 0, 1, 0, 1) // A IN
}

// TestD1Forward 端到端：client → proxy → upstream → 回。
// 验收你 D1 的 query 场景（四跳走通、应答 QR=1 ID 匹配）。
func TestD1Forward(t *testing.T) {
	upAddr, seen := fakeUpstream(t)
	proxyAddr := startProxy(t, Config{Listen: "127.0.0.1:0", Upstream: upAddr.String()})

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()

	query := dnsQuery(42, "example.org")
	if _, err := client.WriteToUDP(query, proxyAddr); err != nil {
		t.Fatalf("send: %v", err)
	}

	buf := make([]byte, 1500)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	h, err := wire.ParseHeader(buf[:n])
	if err != nil {
		t.Fatalf("bad reply: %v", err)
	}
	if h.ID != 42 || !h.QR || h.RCode != 0 {
		t.Fatalf("reply wrong: %+v", h)
	}
	// 上游确实收到了。
	select {
	case <-seen:
	default:
		t.Fatal("query never reached upstream")
	}
}

// TestD1Sinkhole：SINKHOLE 前缀命中 → 本地 NXDOMAIN，上游不见包；
// 非命中前缀照常转发（对照组）。对照你 D1 的 block 场景。
func TestD1Sinkhole(t *testing.T) {
	upAddr, seen := fakeUpstream(t)
	proxyAddr := startProxy(t, Config{
		Listen:   "127.0.0.1:0",
		Upstream: upAddr.String(),
		Sinkhole: "blocked.",
	})

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()
	buf := make([]byte, 1500)

	// 命中前缀：blocked.example → 本地 NXDOMAIN。
	if _, err := client.WriteToUDP(dnsQuery(7, "blocked.example"), proxyAddr); err != nil {
		t.Fatalf("send: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no sinkhole reply: %v", err)
	}
	h, err := wire.ParseHeader(buf[:n])
	if err != nil {
		t.Fatalf("bad reply: %v", err)
	}
	if !h.QR || h.RCode != 3 || h.ID != 7 {
		t.Fatalf("want NXDOMAIN id=7, got %+v", h)
	}
	select {
	case q := <-seen:
		t.Fatalf("sinkholed query leaked to upstream: % x", q[:8])
	case <-time.After(200 * time.Millisecond):
	}

	// 对照组：open.example 照常转发，上游必须见到。
	if _, err := client.WriteToUDP(dnsQuery(8, "open.example"), proxyAddr); err != nil {
		t.Fatalf("send2: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no forwarded reply: %v", err)
	}
	h2, err := wire.ParseHeader(buf[:n])
	if err != nil {
		t.Fatalf("bad reply2: %v", err)
	}
	if h2.ID != 8 || h2.RCode != 0 {
		t.Fatalf("forward wrong: %+v", h2)
	}
	select {
	case <-seen:
	default:
		t.Fatal("open.example never reached upstream")
	}
}

// TestD1Timeout 上游黑洞 → 2s 超时后 goroutine 退出、client 收不到回包（不挂死）。
// 对照你 L7/L9 的背压直觉：没有 deadline 的转发器 = goroutine 泄漏发生器。
func TestD1Timeout(t *testing.T) {
	blackhole, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("blackhole: %v", err)
	}
	defer blackhole.Close() // 收到也不回

	proxyAddr := startProxy(t, Config{Listen: "127.0.0.1:0", Upstream: blackhole.LocalAddr().String()})

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()

	// 发一个查询然后收 3 秒：不得收到任何回包（黑洞），期间 proxy
	// 的 per-query goroutine 必须在 2s deadline 后自己退出。
	// goroutine 泄漏的间接信号：proxy 挂死后这里 cleanup cancel 也救不了
	// 下一个测试——由 -race + 全套超时兜底。
	if _, err := client.WriteToUDP(dnsQuery(9, "slow.example"), proxyAddr); err != nil {
		t.Fatalf("send: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1500)
	if _, _, err := client.ReadFromUDP(buf); err == nil {
		t.Fatal("blackhole upstream must not produce a reply")
	}
}
