package rank

import "testing"

type fakeSrc struct{ items []Scored }

func (f fakeSrc) Posts() []Scored { return f.items }

// TestTopOrder：Score 降序装填，Top 取前 n（W09-1 K9 的排序语义）。
func TestTopOrder(t *testing.T) {
	r := New(fakeSrc{items: []Scored{
		{PostID: 1, Title: "a", Score: 1},
		{PostID: 2, Title: "b", Score: 5},
		{PostID: 3, Title: "c", Score: 3},
	}}, 0)
	r.Refresh()
	got := r.Top(2)
	if len(got) != 2 || got[0].PostID != 2 || got[1].PostID != 3 {
		t.Fatalf("top2: %v", got)
	}
	all := r.Top(10)
	if len(all) != 3 {
		t.Fatalf("top overflow should clamp, got %d", len(all))
	}
	empty := r.Top(0)
	if len(empty) != 0 {
		t.Fatalf("top0: %v", empty)
	}
}

// TestRefreshTTL：未过期的 Refresh 是幂等跳过（W21-1 K25 启动预加载语义）。
func TestRefreshTTL(t *testing.T) {
	calls := 0
	r := New(counterSrc{fn: func() []Scored {
		calls++
		return []Scored{{PostID: 1, Score: 1}}
	}}, 60_000)
	r.Refresh()
	r.Refresh() // TTL 内第二次调用不该触发真算
	if calls != 1 {
		t.Fatalf("refresh should be idempotent within ttl, calls=%d", calls)
	}
}

type counterSrc struct{ fn func() []Scored }

func (c counterSrc) Posts() []Scored { return c.fn() }

// TestConcurrentRefresh：并发 Refresh 单飞不 panic、结果一致（-race 验证）。
func TestConcurrentRefresh(t *testing.T) {
	r := New(fakeSrc{items: []Scored{{PostID: 1, Score: 9}}}, 0)
	done := make(chan struct{})
	for range 10 {
		go func() {
			r.Refresh()
			done <- struct{}{}
		}()
	}
	for range 10 {
		<-done
	}
	got := r.Top(1)
	if len(got) != 1 || got[0].PostID != 1 {
		t.Fatalf("rank lost: %v", got)
	}
}
