package counter

import (
	"sync"
	"testing"
	"time"
)

func TestLikeUnlikeCount(t *testing.T) {
	m := NewMemoryCounter(nil, time.Hour)
	if err := m.Like("post", 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Like("post", 1); err != nil {
		t.Fatal(err)
	}
	c, _ := m.Count("post", 1)
	if c != 2 {
		t.Fatalf("want 2, got %d", c)
	}
	if err := m.Unlike("post", 1); err != nil {
		t.Fatal(err)
	}
	c, _ = m.Count("post", 1)
	if c != 1 {
		t.Fatalf("want 1, got %d", c)
	}
	// Unlike 下界：多次 Unlike 不出负数
	for range 5 {
		_ = m.Unlike("post", 1)
	}
	c, _ = m.Count("post", 1)
	if c != 0 {
		t.Fatalf("want 0 floor, got %d", c)
	}
	// 未知 key 计 0
	c, _ = m.Count("post", 42)
	if c != 0 {
		t.Fatalf("unknown key want 0, got %d", c)
	}
}

// TestConcurrentLike：50 goroutine 并发 Like 同一帖，计数恰 50（W07-1 K6 原子自增语义）。
func TestConcurrentLike(t *testing.T) {
	m := NewMemoryCounter(nil, time.Hour)
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if err := m.Like("post", 7); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c, _ := m.Count("post", 7)
	if c != n {
		t.Fatalf("lost updates: want %d got %d", n, c)
	}
}

// TestFlush：ticker 到点把脏计数交给 flushFn（W07-2 K25/K26 消费端凑批的调度侧）。
func TestFlush(t *testing.T) {
	flushed := map[string]int64{}
	var mu sync.Mutex
	m := NewMemoryCounter(func(biz string, id int64, cnt int64) error {
		mu.Lock()
		flushed[biz+":"+itoa(id)] = cnt
		mu.Unlock()
		return nil
	}, 10*time.Millisecond)
	_ = m.Like("post", 1)
	_ = m.Like("post", 1)
	_ = m.Like("post", 2)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(flushed)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = m.Close()
	mu.Lock()
	defer mu.Unlock()
	if flushed["post:1"] != 2 || flushed["post:2"] != 1 {
		t.Fatalf("flush wrong: %v", flushed)
	}
}
