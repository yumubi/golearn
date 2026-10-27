# counter-battle — 共享计数器四方案

复刻 rs/rust-for-frontend labs/experiments/concurrency/counters/ 的 Go 版。

## 复现

```bash
cd /path/to/golearn  # 替换为仓库根目录

# 正确性门禁（先于一切压测）
go test -run TestCorrectness -count=1 ./labs/experiments/counter-battle/

# 15 轮战报（min/中位/max/极差）
go test -run TestBattle -count=1 -v ./labs/experiments/counter-battle/

# -race 会显著增加开销；超时本身不能证明死锁，也不能证明没有死锁。
# 用正确性测试检查竞争；排查超时时还需查看测试输出或 goroutine 栈。
go test -race -timeout 300s -run TestCorrectness ./labs/experiments/counter-battle/
```

## 结果

见 ../results-2026-10-10.md E2。
