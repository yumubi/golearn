---
title: 毕业项目 dnsproxi-go
---

# 毕业项目：dnsproxi-go

> DNS UDP 代理：wire 解析 → 转发 → 缓存 → 逐出 → TinyLFU 准入 → 主动老化。
> 对照同构的 Rust 版（本机 `rs/dnsproxi`）。

## 它在哪条弧上

- **② 服务搭建**的终点读数：P6 notes 讲架构与逐级构建（D1→D7）。
- **③ 可观测性**的落地现场：P17 指标、P18 日志都长在这个服务上。

## 真机压测读数（同机，dnsq 压测器）

| 场景 | Go qps | Go P50 | Go P99 | Rust qps | Rust P50 | Rust P99 |
|---|---|---|---|---|---|---|
| A 无缓存 keyspace=1000 | 10 924 | 1.25ms | 4.5ms | 12 439 | 0.92ms | 5.1ms |
| B 缓存 on keyspace=1000 | 9 336 | 1.28ms | 6.8ms | 12 156 | 0.65ms | 6.9ms |
| B2 缓存 on keyspace=100 | 9 940 | 1.26ms | 5.6ms | 19 422 | 0.63ms | 2.0ms |
| C 准入 on keyspace=5000 | 10 288 | 1.09ms | 6.9ms | 15 037 | 0.95ms | 2.4ms |
| D +主动老化 | 10 381 | 1.15ms | 6.1ms | 13 596 | 0.97ms | 4.1ms |

三个读数（详细分析见仓库 `dnsproxi-go/LESSON.md`）：

1. **总量级同档**：Go ~10k qps vs Rust ~13-19k。瓶颈在 per-query socket 生命周期，不在语言运行时。
2. **命中路径差距最大**（B2，2×）：Go 版 `RWMutex + 两次拷贝` vs Rust 版 `Arc 读锁 + 一次拷贝`。省一次拷贝是现成优化点，留作练习。
3. **所有档位零丢包**：差异全是代理自身吞吐/延迟，不是网络抖动。

## 指标与日志（③ 阶段的落地形态）

```sh
cd dnsproxi-go
DNS_LISTEN=:15353 DNS_CACHE=1 DNS_LOG_LEVEL=debug go run .
# 另一个终端：
curl -s localhost:9153/metrics | grep dnsproxi
```

| 指标 | label | 说明 |
|---|---|---|
| `queries_total` | qtype×outcome | 量与错误率、命中率；qname 高基数不进 label |
| `upstream_duration_seconds` | 无 | 只量成功转发的上游往返 |
| `upstream_errors_total` | reason（timeout/network/refused） | 死上游→refused（ICMP 立回），黑洞→timeout（挂满 deadline） |
| `in_flight_queries` | 无 | worker pool 模式下顶到 N 即背压点 |
| `cache_entries` | 无 | PutFull 涨、expirer 落，双更新点 |

日志：JSON 结构化，每查询一个 `req_id` 由 ctxHandler 自动注入（P16 手艺在独立 module 的重写），`DNS_LOG_LEVEL` 可调级别。

## 跑测试

```sh
cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...
```
