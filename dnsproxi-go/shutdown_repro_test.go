package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// 复现手册改造题 2：DNS_CACHE_ACTIVE_TTL=1 时优雅关停卡死。
//
// 路径：cancel → ctx.Done() → pc.Close() → 读循环 ReadFromUDP 报错退出 →
// wg.Wait()。expirer goroutine 在 wg 里，它的 for-range 只有在 RunExpirer
// 的内部 goroutine close(stats) 后才退出；而那个 goroutine 等的是 done
// channel，close(done) 挂在 Run 的 defer 上（Run 还没返回，defer 没跑）。
// wg.Wait() 等它退出，它等 Run 的 defer 执行：互等，永久卡死。
//
// 探法：起真实 Run（ActiveTTL=1），然后 cancel，用 sync.WaitGroup 外围等
// Run 返回。修好前这里 2 秒超时；修好后立刻返回。
func TestActiveTTLGracefulShutdown(t *testing.T) {
	blackhole, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("blackhole: %v", err)
	}
	defer blackhole.Close()

	port := freePort(t)
	cfg := Config{
		Listen:    net.JoinHostPort("127.0.0.1", itoa(port)),
		Upstream:  blackhole.LocalAddr().String(),
		CacheOn:   true,
		ActiveTTL: true,
		ExpireHZ:  50,
	}
	p := &Proxy{cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	var once sync.Once
	go func() {
		_ = p.Run(ctx)
		once.Do(func() { close(runDone) })
	}()
	time.Sleep(50 * time.Millisecond) // 等 ListenUDP + expirer 就位

	cancel()
	select {
	case <-runDone:
		// Run 在关停时限内返回。
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after cancel: expirer goroutine deadlocks wg.Wait")
	}
}
