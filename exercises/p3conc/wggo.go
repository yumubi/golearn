package conc

import (
	"sync"
	"testing"
)

// ===== sync.WaitGroup.Go（Go 1.25，T3 特性审计例子） =====

// FanoutSum 用 WaitGroup.Go（1.25+）并发算平方和：
// Go(f) = go f + Add(1) + 回调 Done 的三合一，不再有
// "Add 忘在 goroutine 里"的竞态坑（Add 必须在 Wait 前）。
// 对比 RunPool（本包）：那里手写 Add/Done 三件套。
func FanoutSum(xs []int) int {
	var wg sync.WaitGroup
	results := make([]int64, len(xs))
	for i, x := range xs {
		wg.Go(func() {
			results[i] = int64(x) * int64(x)
		})
	}
	wg.Wait()
	var sum int64
	for _, r := range results {
		sum += r
	}
	return int(sum)
}

// TestWaitGroupGo 1.25 的 wg.Go：并发正确、无 Add/Done 样板。
// 与 RunPool（手写三件套）对照：省 Add(1)/defer Done() 两行，
// 且 Add 在 Go 内部紧跟 goroutine 创建，天然无竞态。
func TestWaitGroupGo(t *testing.T) {
	if got := FanoutSum([]int{1, 2, 3, 4}); got != 30 {
		t.Fatalf("FanoutSum = %d, want 30", got)
	}
	if got := FanoutSum(nil); got != 0 {
		t.Fatalf("FanoutSum(nil) = %d, want 0", got)
	}
}
