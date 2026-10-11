# counter-battle — 共享计数器四方案

复刻 rs/rust-for-frontend labs/experiments/concurrency/counters/ 的 Go 版。

## 复现

```bash
cd /Users/akka/Dev/learning/go

# 正确性门禁（先于一切压测）
go test -run TestCorrectness -count=1 ./labs/experiments/counter-battle/

# 15 轮战报（min/中位/max/极差）
go test -run TestBattle -count=1 -v ./labs/experiments/counter-battle/

# 注意：-race 下 hot 模式 channel 方案每迭代发 800 万条,
# race 检测器通道减速 10-20×, 80s 超时跑不完是**预期行为**,
# 不是死锁。race 只对正确性测试有意义:
go test -race -timeout 300s -run TestCorrectness ./labs/experiments/counter-battle/
```

## 结果

见 ../results-2026-10-10.md E2。
