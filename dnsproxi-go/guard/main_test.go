package guard

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// guardSuite 聚合测试上下文：基时刻 + ctx + 断言助手。
// 每个 test 独立 suite 实例（基时刻相同，时钟各自推进互不干扰）。
type guardSuite struct {
	t    *testing.T
	base time.Time
	ctx  context.Context
}

func (s *guardSuite) failf(format string, args ...any) {
	s.t.Helper()
	s.t.Errorf(format, args...)
}

// runSuite 模式：与仓库 exercises 包一致的子测试驱动。
func TestGuard(t *testing.T) {
	base := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		fn   func(*guardSuite)
	}{
		{"bucket_allow_reject", (*guardSuite).testBucketAllowReject},
		{"bucket_refill_cap", (*guardSuite).testBucketRefillCap},
		{"bucket_wait_n", (*guardSuite).testBucketWaitN},
		{"bucket_wait_n_cancel", (*guardSuite).testBucketWaitNCancel},
		{"bucket_nil_on_bad", (*guardSuite).testBucketNilOnBad},
		{"breaker_trip", (*guardSuite).testBreakerTrip},
		{"breaker_min_samples", (*guardSuite).testBreakerMinSamples},
		{"breaker_half_open_recover", (*guardSuite).testBreakerHalfOpenRecover},
		{"breaker_half_open_retrip", (*guardSuite).testBreakerHalfOpenRetrip},
		{"breaker_window_slide", (*guardSuite).testBreakerWindowSlide},
		{"guard_concurrent", (*guardSuite).testGuardConcurrent},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := &guardSuite{t: t, base: base, ctx: context.Background()}
			tc.fn(s)
		})
	}
}

// TestMain 静默退出码，与仓库其它包一致（无全局资源需清理）。
func TestMain(m *testing.M) { os.Exit(m.Run()) }

var _ = fmt.Sprintf // 保留 fmt：failf 用 any 参数
