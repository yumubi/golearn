# LESSON ： Go 版 dnsproxi 毕业总结（G6 压测收官）

2026-10-10，同机压测（macOS arm64, go1.26.4 / rustc release）。发包器
`dnsq/`（Go 写的 UDP 压测器，`-conc 16 -dur 5s`，fake upstream 为 Go 热循环
回包器）。记录的档位均为零丢包；这不能排除系统调用、调度和其他因素对结果的影响。

## 压测数据

qps 越高越好，P99 越低越好。

| 场景 | Go qps | Go P50 | Go P99 | Rust qps | Rust P50 | Rust P99 |
|---|---|---|---|---|---|---|
| A 无缓存 keyspace=1000 | 10 924 | 1.25ms | 4.5ms | 12 439 | 0.92ms | 5.1ms |
| A2 无缓存 keyspace=100 | 未记录 | 未记录 | 未记录 | 12 953 | 0.96ms | 5.3ms |
| B 缓存 on keyspace=1000 | 9 336 | 1.28ms | 6.8ms | 12 156 | 0.65ms | 6.9ms |
| B2 缓存 on keyspace=100（命中路径） | 9 940 | 1.26ms | 5.6ms | 19 422 | 0.63ms | 2.0ms |
| C 准入 on keyspace=5000 | 10 288 | 1.09ms | 6.9ms | 15 037 | 0.95ms | 2.4ms |
| C2 准入+spread | 9 590 | 1.23ms | 5.8ms | 16 500 | 0.88ms | 2.2ms |
| D +主动老化 | 10 381 | 1.15ms | 6.1ms | 13 596 | 0.97ms | 4.1ms |

## 读数

1. **本次测量的量级**：Go 约 10k qps，Rust 约 13–19k qps。两版均为每查询使用临时上游 socket 和 goroutine/task；这些读数本身不能确定瓶颈位置。
2. **命中路径差距最大**（B2：9.9k vs 19.4k，约 2×）。两版使用不同的锁和拷贝路径，但不能仅凭这个对比断定差距的原因。后续 E4 对拷贝做了单独实验，见下文。
3. **准入和老化档位**：本次 C/D 与 B 的读数没有明显回撤；不同 keyspace 和负载下不能直接外推。
4. **P50 在本次记录中 Rust 版较低**。调度和 GC 可能影响延迟，但这里的数据不足以分别量化其贡献。
5. **测量范围**：dnsq 和 fake upstream 均为自写程序，同机短窗口结果适用于这组配置，不是语言性能的普遍结论。Rust B 档曾出现 591ms P99 离群读数，排查原因需要额外证据。

## 这门课的 take-away（对照 P6 课表）

- **wire/proxy**：手撕 DNS 二进制协议 + UDP 并发模型，Go 的
  goroutine-per-packet 与 tokio spawn-per-packet 结构同构，行数 1:1.2。
- **cache**：Map + RWMutex 对 HashMap + RwLock，语义直译；坑在
  "命中必须升级写锁更新 access"（G3 教训）。
- **evict**：keys+slot 反查表移植成 keys 切片 + `map[Key]int`，
  xorshift/Floyd 抽样位运算逐字同构。
- **tinylfu**：count-min + doorkeeper + aging，`atomic.Uint64` 打包
  16 格 4bit 计数器，两版同构；判决函数 10 行，位运算无翻译成本。
- **active-expire**：Redis activeExpireCycle 移植，锁边界=轮边界；
  25%→10% 的考据是全课最值钱的 30 行。
- **压测**：方法论一句：**先修压测器自己的 bug（dnsq 的 deadline
  不刷新导致 96% 假丢包），再读数据**。压测器错了，一切结论都是噪声。

## 复现

```sh
# fake upstream（从仓库根目录）
go run ./labs/experiments/hitpath-ab/fakeup -port 15300 &
# Go 版
DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 DNS_CACHE=1 \
  DNS_CACHE_MAX=2048 DNS_CACHE_SAMPLE_K=8 DNS_CACHE_ADMIT=tinylfu \
  DNS_CACHE_ACTIVE_TTL=1 go run .            # 从 dnsproxi-go/
# Rust 版
DNS_CACHE=1 DNS_CACHE_MAX=2048 DNS_CACHE_SAMPLE_K=8 DNS_CACHE_ADMIT=tinylfu \
  DNS_CACHE_ACTIVE_TTL=1 target/release/dnsproxi 127.0.0.1:15353 127.0.0.1:15300
# 压测
go run ./dnsq -target 127.0.0.1:15353 -dur 5s -conc 16 -names 5000
```

## P9 附录：实验对毕业结论的三处修正（labs/experiments/results-p9）

P7-P9 三批实验对本 LESSON 的回改：毕业不是终点，实验会修正它：

1. **读数 2 的后续实验**：E4 中省拷贝的微基准有改善，但端到端未测出明显差异；它不能解释本次两版的全部差距。
2. **GC 证据**：E5 trace 记录了 mark assist；其出现表明分配路径参与了 GC 工作，但单凭次数不能确定它对 P50 的贡献。
3. **候选配置而非通用生产建议**：E6 在 macOS、特定 B2 负载和三轮实验中，`DNS_HITPATH_POOL=1 DNS_WORKER_POOL=8` 的 P50 低于逐包 goroutine；吞吐有波动。上线前应以实际流量、资源限制及尾延迟复测，worker 数不能由该实验直接推广。

方法收获：先估计优化路径在端到端中的占比；微基准显著不等于端到端显著。调度形态也可能影响吞吐和尾延迟，需要分别测量。
