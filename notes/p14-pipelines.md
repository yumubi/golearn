# 第 14 章 · errgroup + pipeline：并发的工程化组装

> 这章学完你能干什么：把一段"起 N 个 goroutine 抓数据"的草稿改造成生产形状。并发有上界，首错会传播，下游取消上游会退，跑完没有泄漏，每一条都有测试盯着。
> 读者：读完 P3，会 goroutine、channel、select、ctx。前置：P3。后续：P7 的 `labs/experiments/` 里有这套模式的 worker pool 真机版。
>
> 动手方式：开一个空目录，`go mod init demo`，再 `go get golang.org/x/sync`。每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。

## 体系背景：Unix 管道的背压

先不写 Go，看 shell 里的管道。`yes` 会无限输出 `y`，`head` 只取前 3 行：

```sh
$ yes | head -3
y
y
y
```

`yes` 本来永远不会停，是谁让它停的？`head` 读够 3 行就退出，管道的读端关闭，下一次 `yes` 写入时内核给它发 SIGPIPE 信号。它的退出码是 141，等于 128 加信号编号 13：

```sh
$ yes | head -3 > /dev/null; echo "yes 退出码: ${pipestatus[1]}"
yes 退出码: 141
```

这里的 `pipestatus` 是 zsh 的写法，bash 里是 `PIPESTATUS[0]`。

管道还有一个缓冲区。内核在两个进程之间放了一小块内存，写满了写端就阻塞。用一段 Python 测它有多大：

```sh
$ python3 -c "
import os
r, w = os.pipe()
os.set_blocking(w, False)
n = 0
try:
    while True:
        n += os.write(w, b'x' * 1024)
except BlockingIOError:
    print('管道写满时累计字节:', n)
"
管道写满时累计字节: 65536
```

本机是 65536 字节，`man 7 pipe` 里 Linux 的默认容量也是 64 KiB。缓冲区没满时，写端不受读端影响：

```sh
$ dd if=/dev/zero bs=1024 count=32 2>&1 >/dev/null | tail -1
32768 bytes transferred in 0.000041 secs (799219512 bytes/sec)
```

32 KiB 全写进缓冲区，瞬间完成。现在让读端先睡 1 秒，写 200 KiB，超过了缓冲区：

```sh
$ dd if=/dev/zero bs=1024 count=200 2>/tmp/dd.err | (sleep 1; wc -c); tail -1 /tmp/dd.err
  204800
204800 bytes transferred in 1.003647 secs (204056 bytes/sec)
```

写端被拖慢到 1.003 秒，刚好等读端睡醒。写端的速度被读端的消费速度拉平，这就是背压。

channel 是同一个设计：无缓冲 channel 的容量是 0，`make(chan int, n)` 的缓冲区相当于管道缓冲区，下游关闭或取消相当于读端关闭。这章的 pipeline 就是把 shell 管道搬进进程内。

## errgroup

P3 的 `WaitGroup` 只管"等全部结束"。工程上它缺三件事：不传错误，一个失败了其他还在白跑，也不限并发。

`golang.org/x/sync/errgroup` 补齐这三件。它在官方扩展库里，因为 API 还在演化，但生产已普遍使用。先看首错和联动取消：

```go
g, ctx := errgroup.WithContext(context.Background())
for i := 1; i <= 5; i++ {
	g.Go(func() error {
		if i == 3 {
			time.Sleep(10 * time.Millisecond)
			return errors.New("任务 3 失败")
		}
		select {
		case <-time.After(200 * time.Millisecond):
			fmt.Println("任务", i, "完成")
			return nil
		case <-ctx.Done():
			fmt.Println("任务", i, "被取消:", ctx.Err())
			return ctx.Err()
		}
	})
}
err := g.Wait()
fmt.Println("Wait 返回:", err)
```

```sh
$ go run main.go
任务 5 被取消: context canceled
任务 2 被取消: context canceled
任务 1 被取消: context canceled
任务 4 被取消: context canceled
Wait 返回: 任务 3 失败
```

任务 3 失败后，`WithContext` 派生出的 ctx 被取消，其余四个任务不用等满 200 毫秒就退出了。`Wait()` 返回第一个非 nil 错误。四行取消的顺序每次可能不同。

Kotlin 的 `coroutineScope { launch { } }` 是同一个思路：子协程失败，兄弟全部取消。Java 21 起有预览特性 `StructuredTaskScope`，也是这个模型。JS 的 `Promise.all` 遇到第一个 reject 会立刻返回，但不会取消其余的任务，它们还在后台跑。

## SetLimit

`for ... g.Go(...)` 一次起 N 个会怎样？N 是一万就是一万个 goroutine 打向数据库。`SetLimit(n)` 加一道闸门：

```go
var g errgroup.Group
g.SetLimit(2)
start := time.Now()
for i := 1; i <= 5; i++ {
	g.Go(func() error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	fmt.Printf("%4dms g.Go(%d) 返回\n", time.Since(start).Milliseconds()/10*10, i)
}
g.Wait()
fmt.Printf("%4dms Wait 返回\n", time.Since(start).Milliseconds()/10*10)
```

```sh
$ go run main.go
   0ms g.Go(1) 返回
   0ms g.Go(2) 返回
 100ms g.Go(3) 返回
 100ms g.Go(4) 返回
 200ms g.Go(5) 返回
 300ms Wait 返回
```

前两个立刻返回，第 3 个等到 100 毫秒才返回。这是 `SetLimit` 的一个非显然语义：闸门满时 `g.Go` 会阻塞调用方。

不想阻塞，用 `TryGo`，它返回 `bool`：

```go
for i := 1; i <= 4; i++ {
	ok := g.TryGo(func() error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	fmt.Println("TryGo", i, "->", ok)
}
```

```sh
$ go run main.go
TryGo 1 -> true
TryGo 2 -> true
TryGo 3 -> false
TryGo 4 -> false
```

满员时直接返回 `false`，由你决定跳过还是排队。写代码前想清楚任务该怎么排队，Java 读者可以把 `SetLimit` 当成 `Semaphore`，JS 里对应的是 `p-limit` 这类库。

## 背压

回到体系背景里的 `dd`。把读端换成消费者 goroutine，每个值要 100 毫秒，看无缓冲和缓冲 3 的 channel：

```go
ch := make(chan int, size)
go func() {
	for v := range ch {
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("%4dms 消费 %d\n", elapsed(), v)
	}
	close(done)
}()
for i := 1; i <= 4; i++ {
	ch <- i
	fmt.Printf("%4dms 生产 %d\n", elapsed(), i)
}
close(ch)
```

```sh
$ go run main.go
== 无缓冲
   0ms 生产 1
 100ms 消费 1
 100ms 生产 2
 200ms 消费 2
 200ms 生产 3
 300ms 消费 3
 300ms 生产 4
 400ms 消费 4
== 缓冲 3
   0ms 生产 1
   0ms 生产 2
   0ms 生产 3
   0ms 生产 4
 100ms 消费 1
 200ms 消费 2
 300ms 消费 3
 400ms 消费 4
```

无缓冲时，生产的节奏完全被消费拖着走，和 `dd` 的 1 秒一样。缓冲 3 时，4 个值一瞬间全"生产"完了。这是缓冲区 3 个，加消费者手里已经取走的 1 个。

所以缓冲区是蓄水池，不是加速器。它只在你知道容量上界时才加，否则默认用无缓冲，让背压自然形成。

Node.js 的 stream 里 `write()` 返回 `false` 就是同样的信号，`highWaterMark` 对应缓冲区大小。Kotlin 的 `Flow` 靠挂起实现背压，Java 的有界 `BlockingQueue` 满了也会阻塞生产者。

## 生成器

流水线的第一站是把切片变成 channel。关键是 `select` 里挂上 `ctx.Done()`：

```go
func gen(ctx context.Context, nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, n := range nums {
			select {
			case out <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
```

为什么非挂不可？对比没有 `ctx` 的版本：发送写成 `out <- n`，下游只收 1 个就撤：

```sh
$ go run main.go
基线 goroutine: 1
只收 1 个就撤: 2
```

多出来的那个 goroutine 永远卡在 `out <- n`，没人收，这就是泄漏。channel 世界最常见的泄漏源，就是发送端卡死在没人收的 channel 上。

换成上面带 `ctx` 的 `gen`：

```sh
$ go run main.go ctx
基线 goroutine: 1
收 1 个，还没 cancel: 2
cancel 之后: 1
```

`cancel()` 之后 goroutine 数回到基线。`select ctx.Done()` 是唯一的药。

## 流水线

多个站首尾相接就是流水线。每站一个 goroutine，channel 是站间的传送带，这是 Go blog 的经典模式（Sameer Ajmani，2014-03-13，go.dev/blog/pipelines）。

```go
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

for v := range square(ctx, gen(ctx, 1, 2, 3, 4, 5)) {
	fmt.Print(v, " ")
}
```

```sh
$ go run main.go
1 4 9 16 25
```

练习包里的 `Pipeline` 用 errgroup 把这些串起来。再看一下背压在多站里怎么表现，下游每个结果处理 100 毫秒：

```sh
   0ms 上游发完 1
   0ms 上游发完 2
 100ms 下游处理 1
 100ms 上游发完 3
 200ms 下游处理 4
 200ms 上游发完 4
 300ms 下游处理 9
 400ms 下游处理 16
```

上游没有一口气发完，4 个值花了 200 毫秒才发完。但它领先下游一两个，因为每一站手里都能攥着一个值。站越多，领先量越大，缓冲就是这么藏在站里的。

## 扇出与保序

一站处理太慢，就起多个 worker 同时干，这叫扇出。麻烦在于：并发完成的顺序是乱的，结果怎么保序？

办法很朴素：worker 拿到下标 `i`，直接写 `results[i]`。切片各元素互相独立，无锁也安全：

```go
results := make([]string, len(nums))
var g errgroup.Group
g.SetLimit(3)
for i, n := range nums {
	g.Go(func() error {
		time.Sleep(time.Duration(10-n) * 3 * time.Millisecond) // 大的先完成
		results[i] = fmt.Sprintf("r%d", n)
		fmt.Println("完成", n)
		return nil
	})
}
g.Wait()
fmt.Println(results)
```

```sh
$ go run -race main.go
完成 3
完成 2
完成 1
完成 5
完成 4
完成 6
[r1 r2 r3 r4 r5 r6]
```

完成顺序是乱的，`-race` 没有报告，结果仍然按下标排好。Rust 的 `buffer_unordered` 之后还要 `enumerate` 重排，这个更直接。

练习包里的 `Fanout` 就是这个形状：`SetLimit(consumers)` 控并发，下标写回 `results[i]`。`TestFanoutResultOrder` 和 `TestFanoutConcurrent` 分别验证保序和真的并发。

## 扇入

反过来，多路 channel 合成一路，叫扇入。每路一个转发 goroutine，全部结束后关总 channel：

```go
func fanin(ctx context.Context, ins ...<-chan int) <-chan int {
	out := make(chan int)
	var wg sync.WaitGroup
	for _, in := range ins {
		wg.Go(func() {
			for v := range in {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() { wg.Wait(); close(out) }()
	return out
}

sum, n := 0, 0
for v := range fanin(ctx, gen(ctx, 1, 2, 3), gen(ctx, 10, 20), gen(ctx, 100)) {
	sum += v
	n++
}
fmt.Println("元素数:", n, "总和:", sum)
```

```sh
$ go run main.go
元素数: 6 总和: 136
```

三路共 6 个元素，一个不丢，顺序不保证。练习包里的 `Fanin` 用的是 Go 1.22 以前也能写的 `wg.Add` 加 `go func(c)` 形式，效果一样。

扇入是纯转发，没有错误语义，所以用 `WaitGroup` 而不是 errgroup。按需选工具，errgroup 不是唯一解。

Kotlin 的 `merge()` 流操作符、RxJS 的 `merge` 做的是同一件事。

## 并发上限的消融

`SetLimit(2)` 真的限住了吗？口说无凭，用并发计数器证明（`TestFanoutLimit`）：

```go
var cur, max atomic.Int64
process := func(_ context.Context, n int) (string, error) {
	c := cur.Add(1)
	for { // CAS 循环更新 max
		m := max.Load()
		if c <= m || max.CompareAndSwap(m, c) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond) // 制造重叠窗口
	cur.Add(-1)
	return "ok", nil
}
// SetLimit(2) 跑 8 个任务，max.Load() 必须 <= 2
```

实测 max = 2。把 `Fanout` 里的 `g.SetLimit(consumers)` 那行删掉再跑，这是消融：

```sh
$ go test -race -run TestFanoutLimit -v ./exercises/p14pipe/
=== RUN   TestFanoutLimit
    p14pipe_test.go:83: max concurrent workers = 8, want <= 2
--- FAIL: TestFanoutLimit (0.01s)
FAIL
```

max = 8，8 个任务全部同时在跑。这就是闸门的作用量。消融实验是验证并发参数的唯一可靠方法，P7 的方法论在这里复用。看完用 `git checkout` 还原。

## 首错与泄漏检测

首错语义由 `TestFanoutFirstError` 验证：一个 worker 返回错误，`g.Wait()` 返回它，ctx 被取消，其余 worker 通过 `select ctx.Done()` 提前退出。

在练习包的副本上给 worker 加日志，5 个任务限 2 并发，3 号失败：

```sh
err: worker for 3: boom on 3
[完成 2 完成 1 取消 4]
```

1、2 号跑完了，4 号被取消，5 号根本没起来。第 3 号失败之后，剩余任务没跑完就被拉闸。

泄漏检测由 `TestFanoutGoroutineLeak` 和 `TestGenCancel` 负责：跑完后 `runtime.NumGoroutine()` 回落到基线。把 `gen` 里的 `case <-ctx.Done()` 分支删掉，`TestGenCancel` 立刻失败：

```sh
$ go test -race -run TestGenCancel -v ./exercises/p14pipe/
    p14pipe_test.go:141: goroutine leak after cancel: before=2 after=3
--- FAIL: TestGenCancel (0.11s)
```

这两个测试是 pipeline 代码的体检仪，抄进任何类似代码都值。

## 小结

1. errgroup 补齐 WaitGroup 的三个缺口：错误传播、ctx 联动取消、`SetLimit` 闸门。闸门满时 `g.Go` 会阻塞。
2. 背压来自无缓冲 channel，和 Unix 管道缓冲区写满阻塞是同一个机制。
3. 每个发送 goroutine 都要 `select ctx.Done()`，否则下游一撤你就泄漏。
4. 保序靠下标写回，不靠锁也不靠排序。
5. 并发参数用消融验证，泄漏用 `NumGoroutine` 回落检测。纯转发用 WaitGroup，有错误语义才上 errgroup。

## 练习：p14pipe

```sh
$ go test -race ./exercises/p14pipe/
ok  	golearn/exercises/p14pipe	1.344s
```

8 个测试 `-race` 全绿，覆盖三板斧、并发上限消融、首错传播和 goroutine 泄漏检测。这四件事凑齐了，你就敢把这段模式抄进生产代码。

- `TestPipeline`：gen、square、collect 串起来，结果保序。
- `TestFanoutConcurrent`：fanout 真的并发，不是退化成串行。
- `TestFanoutResultOrder`：下标写回，结果保序。
- `TestFanoutLimit`：消融，`SetLimit(2)` 实测 max 不超过 2。
- `TestFanoutFirstError`：首错传播加 ctx 联动取消。
- `TestFanoutGoroutineLeak`：跑完一轮 `NumGoroutine` 回落。
- `TestGenCancel`：下游提前撤，`gen` 的 goroutine 不泄漏。
- `TestFanin`：三路汇一路，元素不丢。

**预测**，跑之前写答案：

1. `TestFanoutLimit` 里把 `g.SetLimit(2)` 那行删掉，max 会变成多少？为什么不是 7 或 9 这样的随机数？
2. `TestFanoutFirstError` 里 3 号 worker 返回错误后，4、5 号任务是"跑完了才退出"还是"中途退出"？它们靠哪个分支感知取消？
3. `TestGenCancel` 把 gen 里的 `case <-ctx.Done(): return` 删掉，`NumGoroutine` 会回落吗？泄漏的 goroutine 卡在哪一行？

第 1 题在副本上实测 max 是 8，因为任务一共 8 个，每个都睡 5 毫秒，全部重叠。

**观察**：

```sh
go test -race -run TestFanoutLimit -v ./exercises/p14pipe/
go test -race -run TestFanoutGoroutineLeak -v ./exercises/p14pipe/
```

**改造**：给 `Fanout` 加一个 `stage` 中间站，fanout 后接一个 map 阶段再 fanin，验证背压：下游 sleep 100 毫秒时，上游不会一次性算完所有数。提示：在 gen 里放个计数器，观察"已生产"和"已消费"的差值。

## 章末参考

### 什么时候用哪把刀

- 等一组任务结束，无错误语义：`sync.WaitGroup`，最轻。
- 任务可能失败，要首错和取消：errgroup。
- 并发数有上界（DB 池或限流）：`SetLimit`。
- 每个结果都要收集且保序：下标写回 `results[i]`，无锁，`-race` 验证。
- 多路流合成一路：扇入，WaitGroup 关闸。
- 流式处理数据流：pipeline，背压自然形成。

gRPC server、批量下载、ETL 抽取，底层都是这套组装。

### Rust 对照

| Rust | Go | 差异 |
|---|---|---|
| `futures::stream` 的 `buffer_unordered(n)` | fanout 加 fanin | Rust 一步到位给限并发和流式收集，Go 的部件看得见摸得着 |
| `JoinSet` | errgroup | 都收全部结果，但 `JoinSet` 的首错传播要自己遍历 join 结果找 `Err` |
| `select_all` | `Fanin` | 多路合一路 |
| 结果保序 | 下标写回 | `buffer_unordered` 之后要 `enumerate` 重排 |
| 取消靠 drop | 取消靠 ctx | Go 每个发送点必须自己 `select` |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| errgroup 首错取消 | Kotlin `coroutineScope`，Java `StructuredTaskScope`（预览） | `Promise.all` 返回首错但不取消其余 |
| `SetLimit` | `Semaphore` | `p-limit` 一类库 |
| 背压 | 有界 `BlockingQueue`，Kotlin `Flow` 挂起 | stream 的 `write()` 返回 `false` |
| 扇入 | Kotlin `merge()`，RxJS `merge` | `for await` 配合多个异步迭代器 |
| goroutine 泄漏 | 线程或协程泄漏 | 悬空的 Promise |

泄漏检测进阶：P21 的 `synctest` bubble 收尾能自动指认泄漏的 goroutine，是本章 `NumGoroutine` 法的升级版。
