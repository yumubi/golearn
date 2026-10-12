# LESSON ： Go 版 dnsproxi 毕业总结（G6 压测收官）

2026-10-10，同机压测（macOS arm64, go1.26.4 / rustc release）。发包器
`dnsq/`（Go 写的 UDP 压测器，`-conc 16 -dur 5s`，fake upstream 为 Go 热循环
回包器 `/tmp/fakeup.go` 同源逻辑）。所有档位零丢包：读出来的差异全是
代理自身的吞吐/延迟差异，不是网络抖动。

## 压测数据

qps 越高越好，P99 越低越好。

| 场景 | Go qps | Go P50 | Go P99 | Rust qps | Rust P50 | Rust P99 |
|---|---|---|---|---|---|---|
| A 无缓存 keyspace=1000 | 10 924 | 1.25ms | 4.5ms | 12 439 | 0.92ms | 5.1ms |
| A2 无缓存 keyspace=100 | ， | ， | ， | 12 953 | 0.96ms | 5.3ms |
| B 缓存 on keyspace=1000 | 9 336 | 1.28ms | 6.8ms | 12 156 | 0.65ms | 6.9ms |
| B2 缓存 on keyspace=100（命中路径） | 9 940 | 1.26ms | 5.6ms | 19 422 | 0.63ms | 2.0ms |
| C 准入 on keyspace=5000 | 10 288 | 1.09ms | 6.9ms | 15 037 | 0.95ms | 2.4ms |
| C2 准入+spread | 9 590 | 1.23ms | 5.8ms | 16 500 | 0.88ms | 2.2ms |
| D +主动老化 | 10 381 | 1.15ms | 6.1ms | 13 596 | 0.97ms | 4.1ms |

## 读数

1. **总量级同档**：Go ~10k qps，Rust ~13-19k qps，差 1.3-1.9×。两版都是
   "每查询一个临时上游 socket + goroutine/task" 的架构，瓶颈主要在
   per-query socket 生命周期，不在语言运行时。
2. **命中路径差距最大**（B2：9.9k vs 19.4k，2×）。缓存命中在 Rust 版是
   `Arc` 读锁 + 内存拷贝；Go 版是 RWMutex 读锁 + `append([]byte(nil),...)`
   再拷一次（handleQuery 里应答拷贝）。省一次拷贝是 Go 版现成的优化点，
   但毕业项目不做：留作练习题。
3. **准入/老化开销在噪声内**：两版的 C/D 档相对 B 档都没显著回撤。
   TinyLFU 的 4 次 hash + 4 次 Or、老化 ticker 的每秒 10 周期，在
   万级 qps 下都不是瓶颈：这正是 D5/D6 课文"预算内做正确性"的实测注脚。
4. **P50 上 Rust 版稳定更快**（0.6-1.0ms vs 1.1-1.3ms）：tokio 的
   多线程调度器对这种"收包→查表→回包"的极短任务调度更省；Go 的
   goroutine per packet 也有 GC 停顿的底噪（max 延迟里偶发 30-170ms）。
5. **公平性声明**：dnsq 是自写压测器，测的是"这来两版在同机互打"的
   相对差，不是绝对性能认证；fake upstream 也是 Go 写的，对两版同等
   公平。Rust 版 B 档 P99 出现过 591ms 的离群点（tokio 首次调度预热），
   五秒窗口内均值受它污染：比 P99 更该看的是 P50。

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
# fake upstream（Go，任意目录）
go run /tmp/fakeup.go &                      # 127.0.0.1:15300
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

1. **读数 2 部分证伪**：命中路径 2× 差距的主因**不是**那次拷贝
   （E4：micro 快 1.6×，端到端占比 <5% 测不出）。真凶待查：
   goroutine 唤醒链路 vs tokio waker。
2. **读数 4 机制坐实**：GC 底噪有了直接证据：mark assist 8.1 万次/3s
   （E5 trace），分配方被拉去帮 GC 是隐形延迟税。
3. **生产配置建议**（E6 可复现）：`DNS_HITPATH_POOL=1 DNS_WORKER_POOL=8`
   ：零分配命中 + 8 worker，p50 163µs→~118µs（B2 档 -25%，三轮复现），
   qps 持平 90k+。N=8 是与 P 数匹配的档位；N=16 反而 p50 变差。
   （macOS 裸机测得。Go 1.25 起 Linux 容器里 GOMAXPROCS 默认按
   cgroup CPU 限额取值，容器里复跑时先看默认值再对 N。）

方法收获：**先估占比再跑端到端**（<10% 直接预测测不出）；
**micro 显著 ≠ 端到端显著**；**调度形态影响尾延迟，不影响吞吐上限**。
