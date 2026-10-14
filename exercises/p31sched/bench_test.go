// Package p31sched：正文 §6/§4 基准的复现入口（数字出处）。
package p31sched

import (
	"sync"
	"sync/atomic"
	"testing"
)

// ===== 临界区：Mutex vs channel 信号量（正文 §6）=====

var mu sync.Mutex
var sem = make(chan struct{}, 1)

func BenchmarkMutexCritical(b *testing.B) {
	for range b.N {
		mu.Lock()
		mu.Unlock()
	}
}

func BenchmarkChanCritical(b *testing.B) {
	for range b.N {
		sem <- struct{}{}
		<-sem
	}
}

// ===== RWMutex 与 atomic（正文 §6）=====

var rw sync.RWMutex

func BenchmarkRWMutexRead(b *testing.B) {
	for range b.N {
		rw.RLock()
		rw.RUnlock()
	}
}

func BenchmarkRWMutexWrite(b *testing.B) {
	for range b.N {
		rw.Lock()
		rw.Unlock()
	}
}

var cnt atomic.Int64

func BenchmarkAtomicAdd(b *testing.B) {
	for range b.N {
		cnt.Add(1)
	}
}

func BenchmarkAtomicLoad(b *testing.B) {
	for range b.N {
		_ = cnt.Load()
	}
}

// ===== 并行形态（正文 §6 RunParallel 组）=====

var pmu sync.Mutex
var prw sync.RWMutex

func BenchmarkMutexParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pmu.Lock()
			pmu.Unlock()
		}
	})
}

func BenchmarkRWMutexReadParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			prw.RLock()
			prw.RUnlock()
		}
	})
}

func BenchmarkRWMutexWriteParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			prw.Lock()
			prw.Unlock()
		}
	})
}

// ===== channel 当消息队列（正文 §4）=====

func BenchmarkMsgUnbuf(b *testing.B) {
	ch := make(chan int)
	for i := range b.N {
		go func() { ch <- i }()
		<-ch
	}
}

func BenchmarkMsgBuf100(b *testing.B) {
	ch := make(chan int, 100)
	for i := range b.N {
		go func() { ch <- i }()
		<-ch
	}
}

func BenchmarkMsg10x10Unbuf(b *testing.B) {
	for range b.N {
		ch := make(chan int)
		for w := range 10 {
			go func() { ch <- w }()
		}
		for range 10 {
			<-ch
		}
	}
}

func BenchmarkMsg10x10Buf100(b *testing.B) {
	for range b.N {
		ch := make(chan int, 100)
		for w := range 10 {
			go func() { ch <- w }()
		}
		for range 10 {
			<-ch
		}
	}
}
