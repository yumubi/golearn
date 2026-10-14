package guard

import (
	"context"
	"sync"
	"testing"
)

func BenchmarkTokenBucketAllow(b *testing.B) {
	tb := NewTokenBucket(1_000_000, 1_000_000, RealClock)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow(1)
		}
	})
}

func BenchmarkBreakerAllowClosed(b *testing.B) {
	br := NewBreaker(BreakerConfig{}, RealClock)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			br.Allow()
		}
	})
}

func BenchmarkTokenBucketWaitN(b *testing.B) {
	tb := NewTokenBucket(100_000, 100_000, RealClock) // 0.01ms/枚，等待可忽略
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		tb.WaitN(ctx, 1)
	}
}

var _ = sync.Mutex{}
