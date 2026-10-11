// Package p14pipe：P14 的 pipeline 三板斧（生成器/扇出/汇合）+ errgroup 纪律。
//
// 设计对照（notes §14）：
//   - gen：channel 生成器，ctx 取消即关流（对照 Rust iter + tokio stream）
//   - fanout：N 个 worker 抢同一个 channel，errgroup.SetLimit 控并发
//   - fanin：N 路结果汇入一路（对照 futures::stream::select_all）
//
// 全包 -race 可测，零环境依赖。
package p14pipe

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// gen 把切片变成 channel：ctx 取消时关闭流出（背压的源头——下游不收就发不动）。
func gen(ctx context.Context, nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, n := range nums {
			select {
			case out <- n:
			case <-ctx.Done(): // 下游不收 + 上层取消：不发泄漏 goroutine
				return
			}
		}
	}()
	return out
}

// square 是 pipeline 的中间站：每站一个 goroutine，channel 是站间的传送带。
func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for n := range in {
			select {
			case out <- n * n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Pipeline 串起 gen→square→collect，返回结果和首个错误（pipeline 无错则 nil）。
func Pipeline(ctx context.Context, nums ...int) ([]int, error) {
	g, ctx := errgroup.WithContext(ctx)

	sq := square(ctx, gen(ctx, nums...))

	out := make([]int, 0, len(nums))
	g.Go(func() error {
		for v := range sq {
			out = append(out, v)
		}
		return nil
	})
	return out, g.Wait()
}

// Fanout：consumers 个 worker 抢同一个 in channel；每个 worker 独立处理。
// SetLimit(c) 限制同时活的 goroutine 数——errgroup 的并发闸门。
// 返回错误在任意 worker 失败时非 nil，g.Wait() 等全部收尾（无泄漏）。
func Fanout(ctx context.Context, consumers int, process func(context.Context, int) (string, error), nums ...int) ([]string, error) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(consumers)

	results := make([]string, len(nums)) // 下标写回：结果保序，不需要锁
	src := gen(ctx, nums...)

	idx := make(chan int) // 把下标也发给 worker，保证结果能归位
	go func() {
		defer close(idx)
		for i := range nums {
			select {
			case idx <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	for range nums { // 每个数一个任务，SetLimit 决定同时跑几个
		g.Go(func() error {
			select {
			case i, ok := <-idx:
				if !ok {
					return nil
				}
				<-src // 取一个输入（顺序无关，抢就是并发）
				r, err := process(ctx, nums[i])
				if err != nil {
					return fmt.Errorf("worker for %d: %w", nums[i], err)
				}
				results[i] = r
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}
	return results, g.Wait()
}

// Fanin 把多路 channel 合成一路。每路一个 goroutine 转发，waitgroup 关总闸。
// 对照 Rust futures::stream::select_all。
func Fanin(ctx context.Context, ins ...<-chan int) <-chan int {
	out := make(chan int)
	var wg sync.WaitGroup
	wg.Add(len(ins))
	for _, in := range ins {
		go func(c <-chan int) {
			defer wg.Done()
			for v := range c {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}(in)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}
