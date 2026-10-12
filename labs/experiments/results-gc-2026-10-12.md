# Green Tea GC 消融（gc-pressure 三形态）

日期: 2026-10-12
机器: Apple M4 (10 核), macOS (Darwin 25.x), go1.26.4 darwin/arm64
背景: Green Tea GC 在 Go 1.26 进 baseline（默认开），
`$(go env GOROOT)/src/internal/buildcfg/exp.go:88`：
`GreenTeaGC: true`（baseline 块内）。`GOEXPERIMENT=nogreenteagc`
可关回老 GC。

协议: `go test -run xxx -bench . -benchtime=10000x -benchmem -count=5`
（与 `results-2026-10-10.md` E1 同协议，钉次数防时间片差异），
两组各 5 轮。

## 原始数据

默认（Green Tea 开）:

| 形态 | ns/op（5 轮区间） | B/op | allocs/op |
|---|---|---|---|
| Alloc | 167788-183368 | 512001-512009 | 1000 |
| Pool | 158566-163133 | 26025 | 1004 |
| Reuse | 133941-142729 | 0 | 0 |

`GOEXPERIMENT=nogreenteagc`（关回老 GC）:

| 形态 | ns/op（5 轮区间） | B/op | allocs/op |
|---|---|---|---|
| Alloc | 167948-185467 | 512002-512009 | 1000 |
| Pool | 161361-164610 | 26024 | 1004 |
| Reuse | 135850-144122 | 0 | 0 |

## 对照（min-of-5，nogreen 相对 green 的差）

| 形态 | Green Tea | 老 GC | 差 |
|---|---|---|---|
| Alloc | 167788 | 167948 | +0.1% |
| Pool | 158566 | 161361 | +1.8% |
| Reuse | 133941 | 135850 | +1.4% |

## 解读

- 本实验的形态下两者区间完全交叠（Green Tea 的 min 还略快 0.1-1.8%），
  单轮差被轮间噪声（±5%）吞掉。
- 机制上不意外：alloc 型微基准的税主要是分配与扫描本身，Green Tea 的
  收益点在 mark 阶段的局部性（cache 友好的 span 遍历），10000x 短循环
  里活对象集太小，mark 账差异摊不出来。老 GC 关掉也不会更差，说明
  baseline 切换在这类负载上无痛。
- 复现:

```sh
cd /Users/akka/Dev/learning/go
go test -run xxx -bench . -benchtime=10000x -benchmem -count=5 \
  ./labs/experiments/gc-pressure/
GOEXPERIMENT=nogreenteagc go test -run xxx -bench . -benchtime=10000x \
  -benchmem -count=5 ./labs/experiments/gc-pressure/
```

- 局限: 单机 macOS；容器/大活堆（数百 MB 驻留、多 P）下 Green Tea 的
  差距才可能拉开，本仓没有那种负载的 lab，不做 extrapolation。
