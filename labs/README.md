# labs — golearn 工程实验

方法论照搬 `rs/rust-for-frontend/labs/`：**结论必须出自可复现实验，每个数字有出处**。
golearn 的实验对象是 dnsproxi-go（P6 毕业项目）与 exercises 载体。

## 目录

| 目录 | 是什么 | 对应 |
|------|--------|------|
| `experiments/gc-pressure/` | GC 压力账：分配率三方案（裸分配 / sync.Pool / 零分配）+ gctrace 现场记录 | P7 · GC vs 所有权主线 |
| `experiments/counter-battle/` | 共享计数器四方案：Mutex / atomic / channel / 分片，20 轮配对 + 离散度分析 | P3 并发延伸 |
| `experiments/bench-hygiene/` | benchmark 卫生：防折叠、b.ReportAllocs、交错协议、小样本陷阱 | 方法论 |
| `experiments/hitpath-ab/` | dnsproxi-go 命中路径省拷贝 A/B（micro + 端到端）+ GOMAXPROCS 消融 + trace 调度账 | P6 LESSON 遗留优化点 · P8 |

实测记录：[experiments/results-2026-10-10.md](experiments/results-2026-10-10.md)（P7 三组）·
[experiments/results-p8-2026-10-10.md](experiments/results-p8-2026-10-10.md)（P8 两组）·
[experiments/results-p9-2026-10-10.md](experiments/results-p9-2026-10-10.md)（P9 worker pool + 四实验因果链总表）。

## 约定（照抄 rust-for-frontend labs/README.md）

- 每个实验目录必须有：可一键复现的脚本/命令 + 带日期的 results 文件
  （环境指纹、协议、原始数据、机制解释）。
- 正文引用的每个数字都能在 results 文件里找到出处。
- 生成产物不入库（go.mod 例外：本仓库已有）。

## 快速开始

```bash
cd /path/to/golearn  # 替换为仓库根目录
go test -bench . -benchmem ./labs/...
```
