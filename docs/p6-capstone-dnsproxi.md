---
title: P6 · Capstone：go 版 dnsproxi（对照你的 Rust 载体 D1-D7）
---

# P6 · Capstone：go 版 dnsproxi（对照你的 Rust 载体 D1-D7）


> 你在 `_rust_learning` 线⑤用 Rust 把 dnsproxi 从 D1 写到 D7（负缓存、
> TinyLFU 准入、主动老化，24 个单测、全部实测）。现在用 Go 复刻，
> **每一课都有你自己的 Rust 代码做对照**：这是这套课的收官项目。

## 怎么读这章

这是一份工程复盘日志，不是教程：G1 到 G6 每一段都是一次实测验收
（跑通什么、数字是多少）加教训（翻车在哪、修法是什么）。按 G1→G6
顺序读，起手小节告诉你下一步装什么，复盘小节给实拍表格和 Go vs Rust
实感。文中的阶段规划表保留原样；"对照你 Rust 版"全部指向
_rust_learning 线⑤的 D1-D7 代码。复盘内容未改写，历史价值在原话。

## 为什么是 dnsproxi

- **UDP 无连接 + 每查询临时 socket**（D1）→ goroutine 生死管理和 channel 的天然练习
- **共享缓存**（D2-D7）→ P3 的 sync 决策树 + 你 D4 sample-K、D5 FreqSketch 在
  Go 里重写一遍（`atomic.Pointer` 换 ArcSwap，`sync.Map`/分片锁换 RwLock）
- **TTL 主动老化**（D6）→ time.Ticker + 后台 goroutine 的生命周期管理
- **字节解析**（DNS 报文头、压缩指针）→ Go 的 `encoding/binary` +
  `[]byte` 切片，对照 Rust 的 `&[u8]` + try_get：**没有借用检查的世界里
  怎么安全切 buffer**，这是 Go 的核心功课

## 阶段规划（对应你的 Rust 课程线）

| 阶段 | 内容 | 对照 Rust | 新学 Go 点 |
|---|---|---|---|
| G1 | UDP 转发 + sinkhole | D1 | `net.ListenUDP`、`ReadFromUDP`、per-query goroutine、`net.Resolver` |
| G2 | 应答缓存 (qname,qtype)+TTL 倒计时 | D2 | `map`+`sync.RWMutex`、`time.Now` 单调钟 |
| G3 | 容量上限 + sample-K 逐出 | D3/D4 | `atomic.Int64`、`math/rand`、slots 索引 |
| G4 | TinyLFU 准入（CMS+挡板+老化） | D5 | 位图操作 `[]uint64`、CAS |
| G5 | TTL 主动老化 + 负缓存 | D6/D7 | `time.Ticker`、优雅关停（P3/P5 套路） |
| G6 | 同机压测：Go vs Rust dnsproxi | L8 方法论 | `go test -bench`、pprof |

**验收标准**（沿用你的规矩）：每阶段 `go test` 全绿 + `run.sh` 场景可复跑 +
数字能对上你 Rust 版的量级（否则查为什么，这就是 Lesson）。

## 骨架已就位

`dnsproxi-go/`（module `golearn/dnsproxi-go`）：
- `main.go`：启动参数（env 风格与你 Rust 版一致：`DNS_UPSTREAM`、`DNS_LISTEN`）
- `wire/`：DNS 报文解析（header/question/rr），**练习从这开始**
- `proxy.go`：D1 转发器骨架 + TODO
- `proxy_test.go`：先红后绿的验收测试：起真实 upstream（本地 UDP socket），
  转发、sinkhole、回包断言

## D1 验收场景（对应你 Rust 版 run.sh query/wire/block）

```sh
go test ./dnsproxi-go/...          # 单测全绿
go run ./dnsproxi-go &             # 起 :5353
dig @127.0.0.1 -p 5353 example.com # 走通四跳
```

## 对照阅读表（写 Go 时开着你的 Rust 源码）

| Go 文件 | Rust 对照 | 关注点 |
|---|---|---|
| `wire/header.go` | `parse_header` | `binary.BigEndian.Uint16` vs `u16::from_be_bytes` |
| `wire/name.go` | 压缩指针解析 | `[]byte` 无借用检查：offset 越界=panic，要显式防御 |
| `proxy.go` | `main.rs` 转发循环 | goroutine 泄漏检测（`-race` + `goleak`） |
| `cache/cache.go` | `CacheStore` | `sync.RWMutex` vs `RwLock<HashMap>`，锁粒度选择 |
| `cache/sketch.go` | `FreqSketch` | 位运算一样，但 Go 没有.checked_add，要手动防溢出 |

## G2 复盘（2026-10-09 实测完成）

实装范围：`cache/` 包（Key/Entry/Cache + ParseQuestion/Records/Request/
PlanResponse + Reply）+ main.go 接入（DNS_CACHE=1 开关、命中不转发、
上游应答入库）+ run.sh cache 冒烟场景（fake upstream 加 per-name 计数）。

### 实测结果

| 场景 | 结果 |
|---|---|
| `go test -race -count=1 ./cache/` | 10 测全绿（含并发读写 race 检查） |
| dig#1（miss） | 上游 `[upstream] cache.test type=1 hit=1`，应答 TTL 60 |
| dig#2（1s 后） | **代理日志 `[hit] ttl_left=59s`，dig 显示 TTL 59，上游零包** |
| dig#3（62s 后） | 已过期 → 上游 hit=2，TTL 回到 60 |
| 裸 UDP 客户端（python） | 第 2 查即命中，上游只见 1 包 |

### Go vs Rust 实感（对照你 Rust 版 D2）

1. **解析器从"函数返回 Option"变成"结构体 + error"**。Rust 版
   `question()/records()` 返回 `Option<(String,u16,usize)>`，一行 `?` 走完；
   Go 版 ParseQuestion 返回 `*Question` 结构体，每个字段显式赋值。代码长
   30%，但调用点可读性反而高：`q.Name` vs `q.0`。
2. **Entry 是纯值类型，不需要 AtomicU64**。Rust 版 last_access 是
   `AtomicU64`（读锁下原子写，近似 LRU）；Go 版 D2 档直接砍掉这个字段，
   惰性过期 + RWMutex 读路径零写。AtomicU64 是 D3 sample-K 的前置，到时候
   Go 用 sync/atomic 照抄 fetch_max 语义。
3. **TTL 倒计时的偏移不变量，两个语言都要手工维护**。`question 段长度
   必须相等，否则指针偏移错位` 这个判据，Rust 用 `end != old_end` 三行，
   Go 同样三行。跨语言教训：**字节级协议的不变量检查是语言无关的**，
   谁都逃不掉，谁都不能偷懒。
4. **冒烟踩坑（比 Rust D2 深一层）**：macOS dig 9.10.6 默认置 EDNS OPT
   （ARCOUNT=1）+ **AD 位**（byte3=0x20）。+noedns 消 EDNS、+cd 消不掉
   AD（只加 CD），**+noad 才是"纯 RD=1 查询"**。cache.Request 拒收
   flags2!=0 是刻意的（对齐你 Rust 版 `buf[3] != 0`），冒烟脚本的 dig
   参数就是这节课的作业答案。定位手法：裸 python 客户端一发即中（hit），
   dig 两发全 miss → 差异在客户端 flags → hex dump 上游收到的包
   （`0120` vs `0100`）→ 对表 AD 位。
5. **fake upstream 升级为带计数**。`collections.Counter` per-(qname,qtype)
   计数，每次查询打 `[upstream] qname type=T hit=N`。这行输出就是缓存
   端到端的**判据**（上游只见 2 次 = miss + 过期重取），对应你 Rust 版
   run.sh 里 dnsq.py 的计数逻辑。测试基础设施一开始就为可观测性服务。

### G3 起手

对照你 Rust 版 main.rs 的 CacheStore/keys+slots：D3 容量上限 + sample-K
逐出。Go 版 keys 用 `[]Key` + `map[Key]int`（slot 反查），swap_remove
语义 Go 切片直接对应。入口：`cache/evict.go` + `DNS_CACHE_MAX`/
`DNS_CACHE_SAMPLE_K` 两个 env。

## 收官

G6 做完，把 Go 版和 Rust 版同机压测（wrk 定制 DNS qps 或 dnsq.py 复用），
结论写进 `dnsproxi-go/LESSON.md`，那就是你这门课的毕业论文。

## G4 复盘（2026-10-09 实测完成）

实装范围：`cache/sketch.go`（FreqSketch：CMS 4 行 × 4bit、16 格打包
uint64、spread 雪崩哈希、doorkeeper bloom、老化减半 + size 修正、
Admit 判决）+ PutFull 两值返回（Eviction/Rejected）+ main.go 接线
（DNS_CACHE_ADMIT/DOORKEEPER env、[reject] 日志、命中也计数）。

### 实测结果

| 场景 | 结果 |
|---|---|
| `go test -race -count=1 ./cache/` | 全绿（9 个 G4 测试：计数/饱和/取min/挡板/减半/判决/死人不守门/门控/并发） |
| admit 冒烟：hot1/hot2 各 5 查养热 | 频率到 5，hit 全中 |
| 冷名 cold.test 撞满仓 | `[reject] cold.test (freq 1 <= victim 5，没进缓存)`，**两次都被拒** |
| 缓存完整性 | 被拒插入后 hot1/hot2 原封不动（缓存一字未动的语义落地） |
| 上游计数 | hot1×1、hot2×1、cold×2，准入生效的判据 |

### Go vs Rust 实感（对照你 Rust 版 D5）

1. **AtomicU64 数组 → atomic.Uint64 切片**。Rust `Vec<AtomicU64>` 一行；
   Go `make([]atomic.Uint64, n)` 同样直白（atomic.Uint64 是 Go 1.19+ 的
   对应物，别用 `[]uint64`+atomic.LoadPointer 自虐）。CAS 循环照抄：
   `CompareAndSwap(cur, cur+1<<shift)`，饱和判据 `(cur>>shift)&0xF == 0xF`。
   位运算部分两语言逐字同构，你 Rust 课文说"位运算一样"，实测如此。
2. **fetch_update → Load+Swap**。Rust 减半用 `fetch_update`（CAS 到成功）；
   Go 版我直接 `Swap((Load()>>1)&mask)`，这里没有 ABA 风险（减半是幂等
   收敛的，swap 丢一次并发增量可接受），换来代码少一层闭包。**权衡有据
   才能省**，不是偷懒。
3. **pickVictim 的 expired 判据**。你 Rust 版是
   `DNS_CACHE_VICTIM_EXPIRED_FIRST` 旋钮 + 排序键字典序；Go 版简化成
   "始终检查挑中者死活"（排序仍纯 last_access）。少一个旋钮、语义同向：
   尸体在准入里无条件输。D6 上主动老化时再评估要不要补字典序。
4. **翻车×2，都被测试抓住**：① `pickVictim` 里调了两次 `sampleIndices`
   （一次建 candidates、一次换算下标），RNG 前进导致两个不同的样本，
   下标换算全乱。修法：只调一次，统一成"候选下标集合"。教训同 D3：
   **RNG 是状态机，调两次就漂移**。② NewSketch 的 doorMask 三目表达式
   手滑写进 doorMark 函数体，gofmt 过了但 vet 报语法错，说明**格式化
   工具不背语法正确性的锅**。
5. **准入的语义红利在日志里看得见**：`[reject] cold.test (freq 1 <=
   victim 5，没进缓存)` 这行日志同时给出了判决依据（双方频率）和结果。
   可观测性不是事后加的，是判决函数顺手返回的（Admit 只返回 bool，
   频率在 PutFull 里已知，日志信息零成本）。

### G5 起手

对照你 Rust 版 D6：主动老化（Redis activeExpireCycle）。Go 版
`cache/expire.go`：time.Ticker 每 1/hz 秒醒，每轮随机抽 sample 个 slot
当场判当场删（**一次抽一个，不能先攒批再删**：swap_remove 会让记住的
下标指向别人，静默删活人），25% 过期率高就再来一轮，墙钟预算用完就停。
入口：`DNS_CACHE_ACTIVE_TTL`/`DNS_CACHE_EXPIRE_HZ`。

## G5 复盘（2026-10-10 实测完成）

实装范围：`cache/expire.go`（ExpireRound/ExpireCycle/RunExpirer/Ghosts +
ExpireCfg 默认值考据）+ Cache 账本扩展（ExpiredActive/ExpireChecked/
ExpireCycles/ExpireRounds/ExpireUs）+ main.go 接线（DNS_CACHE_ACTIVE_TTL
等 6 个 env、envIntClamp 越界回退、[expire] 日志、defer 关停链）+
run.sh expire 场景。

### 实测结果

| 场景 | 结果 |
|---|---|
| `go test -race -count=1 ./cache/` | 全绿（7 个 G5 测试：只收尸体/换位陷阱 800 条压测/空表/sample=0/雪崩多轮/低过期率一轮停/关停/并发不 race） |
| 雪崩测试 | 300 条全死 → 多轮清空，`rounds>=2`（10% 判据触发续轮） |
| 低过期率 | 100 活 + 1 死 → `rounds==1`（自适应生效：低 stale 不空转） |
| expire 冒烟 | ghost.test 首查 miss→上游 hit=1；62s 后 `[expire] rounds=2 checked=1 removed=1`；再查 miss→上游 hit=2（尸体确认被收走） |
| 全仓回归 | go vet + `go test -race ./...` 三包全绿，gofmt 零 diff |

### Go vs Rust 实感（对照你 Rust 版 D6）

1. **结构同构，goroutine 替代 tokio::spawn**。你 Rust 版 `tokio::spawn`
   + `interval(Duration::from_millis(1000/hz))`；Go 版 `time.NewTicker`
   + goroutine + `done` channel 关停。间隔算术两版同款 `1000/hz` 毫秒
   ：你 Rust :846 注释"hz>1000 除零炸 ticker"的坑 Go 版一样有
   （`time.Second / Duration(hz)`，hz=0 tick 间隔无穷大 = 永不醒），
   所以 envIntClamp 1..500 的夹取不能省。
2. **锁边界翻译得更直白**。Rust 版 `let (checked, removed) = { let mut c = cache.write(); c.expire_round(...) };`：花括号就是锁的生死线。
   Go 版 ExpireRound 自带 `Lock/defer Unlock`，多轮之间锁天然释放，
   排队的查询能插进来。语言没有作用域锁的语法糖，反而逼你把边界
   想清楚：每轮一个函数，锁的边界 = 函数边界。
3. **Pct 默认 10 不是 25**。你 Rust :857-864 的源码考据先行：25 是
   SLOW_TIME_PERC（CPU 预算百分比），现行 Redis 判据是
   ACCEPTABLE_STALE=10，坊间的 25% 是两个常量张冠李戴。Go 版注释
   直接引用这个考据：先查证再写默认值。
4. **测试翻车×1：不是 bug，是抽样统计**。换位陷阱测试期望 50 轮收干净
   200 具尸体，实测 removed=163：有放回 20 抽/轮 × 50 轮 = 1000 抽，
   本来就覆盖不满 200/800 密度的全部尸体（探针证实 alive 600 条无一
   误伤）。轮数拉到 500 全绿。**教训：断言统计目标前先算覆盖概率**
   差点把"没删完"误判成"误删活人"，方向正好相反。
5. **Ghosts() 的成本观两版通用**。Rust :995 注释原话"没有它，主动老化
   其实没跑起来会是一次安静的失效"。Go 版同款 RLock 全表扫，测试
   与调试用，生产别开（ghost_gauge 默认关的理由）。

### G6 起手（收官）

Go 版 vs Rust 版同机压测（wrk 定制 DNS qps），结论进
`dnsproxi-go/LESSON.md`。对照你 Rust 版的性能预算：每查询一次
`Sketch.Increment`（4 次 Or + 4 次哈希）+ PutFull 锁内判决。压测维度：
无缓存/有缓存/准入 on/off/老化 on/off 五档 qps 与 P99，Rust/Go 各跑。

## G6 复盘（2026-10-10 实测完成，全课收官）

实装范围：`dnsq/`（Go 版 UDP 压测器：并发 worker、rate 限速、
P50/P90/P99 分位、PCG 种子）+ 五档同机压测 + `dnsproxi-go/LESSON.md`
毕业总结。

### 实测结果（conc=16, dur=5s, 全档零丢包）

| 场景 | Go qps/P99 | Rust qps/P99 |
|---|---|---|
| 无缓存 ks=1000 | 10 924 / 4.5ms | 12 439 / 5.1ms |
| 缓存 on ks=1000 | 9 336 / 6.8ms | 12 156 / 6.9ms |
| 命中路径 ks=100 | 9 940 / 5.6ms | 19 422 / 2.0ms |
| 准入 on ks=5000 | 10 288 / 6.9ms | 15 037 / 2.4ms |
| +主动老化 | 10 381 / 6.1ms | 13 596 / 4.1ms |

结论四条（细节在 LESSON.md）：
1. 总量级同档（Go ~10k，Rust ~13-19k，1.3-1.9×），瓶颈都在 per-query
   临时上游 socket，不在语言。
2. **命中路径差距最大（2×）**：Go 版 handleQuery 多一次应答拷贝
   （`append([]byte(nil),...)`），是现成的优化练习。
3. 准入/老化的开销在噪声内：TinyLFU 4 hash + 4 Or、每秒 10 老化
   周期，万级 qps 下都不是瓶颈（D5/D6 "预算内做正确性"的实测注脚）。
4. P50 Rust 稳定快 0.3-0.6ms；Go 偶发 GC 停顿进 max（30-170ms 离群）。

**翻车×2（都在压测器，不在被测系统）**：① dnsq 的
`SetReadDeadline` 只设一次，第 2 包起 deadline 已过期 → 96% 假丢包，
差点把"压测器坏了"读成"代理不行"；② rate 公式把 dur 当分母（应为
`conc/rate` 秒/worker），限速 2000 实发 371。**教训：压测器自己先要
过单 worker 零丢失这一关，再谈并发数据。**

### 收官清单

- [x] G1 wire/proxy（对照 D1）
- [x] G2 cache（对照 D2）
- [x] G3 容量+逐出（对照 D3/D4）
- [x] G4 TinyLFU（对照 D5）
- [x] G5 主动老化（对照 D6）
- [x] G6 压测收官（对照 D7/L8）
- [ ] P1-P5 练习包仍红着，等你动手：capstone 全绿不代表练习过关，
      那些才是你自己写的部分。

## G3 复盘（2026-10-09 实测完成）

实装范围：`cache/evict.go`（keys+slot 反查表、xorshift64*、Floyd 抽样、
pickVictim/PutFull/removeSlot）+ `Cache` 结构扩展（max/SampleK/Seed +
evictions/examined/admits/rejects 账本）+ main.go 接线（DNS_CACHE_MAX/
SAMPLE_K/SEED 三个 env、[evict] 日志）+ run.sh evict 场景。

### 实测结果

| 场景 | 结果 |
|---|---|
| `go test -race -count=1 ./cache/` | 全绿（含 7 个 G3 测试：封顶/最冷者死/在位者刷新/无界/slot 一致性/并发） |
| evict 冒烟：max=2，查 ev1/ev2/ev3 | `[evict] ev1 → ev3 (policy=full-lru checked=2)`，逐出链 ev1→ev2→ev3 |
| 上游计数 | ev1×2、ev2×2、ev3×1，被逐出的名字重新走上游，正是逐出生效的判据 |
| 在位者刷新 | 50 连刷零逐出零拒绝（对照你 Rust 版 :1150 的语义） |

### Go vs Rust 实感（对照你 Rust 版 D3/D4）

1. **Entry.slot 字段 → 反向 map**。Rust 把 slot 存在 Entry 里（逐出时
   swap_remove + 修正被换位条目的 slot，一处写）；Go 版用 `map[Key]int`
   反查表（同样一处写，但多一张表要同步）。三张表（keys/slot/m）必须
   一起动，第一版 removeSlot 漏删 m，len=4 keys=3 m=4，容量上限静默失效，
   被测试当场抓住。教训：**Rust 的 struct 字段是编译器帮你绑定的不变量，
   Go 的平行 map 是你自己守的**。
2. **AtomicU64 → 写锁升级**。Rust 版命中时在读锁下 `fetch_max` 更新
   last_access（"读变写"难题的原子解）；Go 的 map 没有原子，Get 命中
   必须升级写锁。这就是你 Rust 课文 §"严格 LRU"那段说的代价在 Go 的
   实相：**没有原子可借时，读多写少的 RwLock 优势直接打折**。第一版我
   把 touch 留在 PutFull、Get 只读，端到端立刻翻车：ev2 刚命中却被逐出
   （它的 last_access 还停在插入时刻）。"命中必须变热"不是优化，是 LRU
   的定义。
3. **Floyd 抽样一行不差地抄**。`for j in len-k..len { candidate = random(j+1); if dup { j } else { candidate } }`
   ：Rust 和 Go 逐字对应。这类算法
   的正确策略就是信源码，别"用自己的话重写"。
4. **fake clock 注入**。测试用 `NowMs = func() uint64 { return fc.ms }`
   替换逻辑时钟，last_access 完全可控。你 Rust 版测试传 now_ms 闭包；
   Go 的包级 var 替换更粗暴（测试串行时才安全），但够用。

### G4 起手

对照你 Rust 版 FreqSketch：TinyLFU 准入。Go 版 `cache/sketch.go`：
count-min sketch（4 行 × 4bit 计数器，16 格打包进一个 uint64，位运算
Go/Rust 完全同构）+ doorkeeper bloom + 老化减半。入口：`DNS_CACHE_ADMIT`
env。关键判据：候选人频率 vs 在位者频率，打不过就拒（rejects++）。
## G1 复盘（2026-10-09 实测完成）

实装范围：wire（header/name 压缩指针）、proxy（Run 循环、handleQuery、
sinkhole、上游 2s deadline）、fake upstream + dig 冒烟（run.sh）。

### 实测结果

| 场景 | 结果 |
|---|---|
| `go test -race ./...`（wire 5 测 + proxy 3 测） | 全绿，race 0 |
| dig 走 proxy 查 open.test → fake 上游 | NOERROR + `A 10.0.0.1`（四跳通） |
| `DNS_SINKHOLE=blocked.` 查 blocked.test | **NXDOMAIN，上游零包** |
| 对照组 open.test 同实例 | NOERROR（前缀匹配不误伤） |
| 上游黑洞 | handleQuery 2s deadline 后返回，无 goroutine 泄漏 |

### Go vs Rust 实感（对照你 Rust 版 dnsproxi 同功能）

1. **报文解析没有 try_get 兜底**。Rust 版越界 = `Err`，Go 版越界 = panic。
   ParseName 每个 `b[pos]` 前都要手写边界检查，"没有借用检查，防御
   显式化"的落地样本（防御行数 ≈ 解析逻辑行数，Rust 版约 1:0.5）。
2. **per-query 生命周期靠纪律**。`tokio::spawn` 有 JoinHandle 管；
   `go handleQuery()` 谁也不等：Run 里 WaitGroup + ctx-close-监听口
   + 入口 recover 三件套缺一不可。P3 问题 1 的实弹版（goroutine panic 防御）。
3. **UDP deadline 是救命的**。上游黑洞场景，没有 `SetDeadline` 的版本
   goroutine 永久挂死（TestD1Timeout 3s 抓到）。L9 的 "bound your
   resources" 在 Go 的对应物 = 每个 socket 操作都带 deadline。
4. **冒烟踩坑**：fake upstream 把整条 query 尾部（含 EDNS OPT）echo
   回去再拼 answer、ARCOUNT 写 0 → dig 报 "16 extra bytes / malformed"。
   hex dump + 手数偏移定位（多的 16 字节正好是拼上的 A 记录）。
   修复 = 只 echo question 段。字节级协议调试方法论从 Rust D1 直接迁移。
5. **端口坑**：macOS Chrome 常驻 UDP :5353（mDNS），run.sh 改用
   15353/15300。对应 rs-lab 速查表"环境本身要先标定"：本机端口占用
   先 `netstat -anv -p udp` 看一眼。

### 遗留（进 G2 前不必修）

- QDCount>1 的查询 sinkhole 只看第一条 question（D1 语义已够）
- ctx 取消后在飞 query 靠 WaitGroup 排空；G5 加主动老化时统一改
  errgroup + 带超时 drain

