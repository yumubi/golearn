package main

import (
	"os"
	"runtime/trace"
	"testing"
	"time"

	"golearn/dnsproxi-go/cache"
)

// TestTraceCapture 实验 5 的数据采集端：8 goroutine 高频打缓存命中路径
// （Get + ReplyPool + TTL 改写），采 3 秒执行 trace 供 go tool trace 分析。
//
// 复现: cd dnsproxi-go && go test -run TestTraceCapture ./ -v
// 分析: go tool trace /tmp/proxy.trace
func TestTraceCapture(t *testing.T) {
	f, err := os.Create("/tmp/proxy.trace")
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Start(f); err != nil {
		t.Fatal(err)
	}
	defer trace.Stop()

	c := cache.New()
	// 预填 100 个条目（keyspace=100 同 P6 B2 档）
	var lastEntry *cache.Entry
	for i := range 100 {
		name := "n" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".test"
		lastEntry = &cache.Entry{
			Resp:   make([]byte, 64),
			At:     time.Now(),
			MinTTL: 300,
		}
		c.Put(cache.Key{Name: name, Type: 1}, lastEntry)
	}

	stop := make(chan struct{})
	for range 8 {
		go func() {
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := cache.Key{Name: "naa.test", Type: 1}
				if e, ok := c.Get(k); ok {
					q := make([]byte, 64)
					if resp := e.ReplyPool(q, *cache.BufPool.Get().(*[]byte)); resp != nil {
						_ = resp
					}
				}
				i++
				_ = i
				_ = lastEntry
			}
		}()
	}
	time.Sleep(3 * time.Second)
	close(stop)
}
