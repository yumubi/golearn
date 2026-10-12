# T4 审计：版本新鲜度与实测校正

> PLAN-OMP.md T4 产出。三项实验各自核实 + 实测 + 笔记落点。
> 核实基准：go1.26.4 darwin/arm64。

## 总表

| 项 | 事实来源 | 实测 | 笔记落点 | 状态 |
|---|---|---|---|---|
| 1. GOMAXPROCS 1.25 cgroups | `$(go env GOROOT)/src/runtime/cgroup_linux.go`（cgroup-aware 默认值）+ `cgroup_linux_test.go:41`（containermaxprocs GODEBUG） | 源码核实（Linux only，macOS 不适用） | `labs/experiments/results-p8-2026-10-10.md` L77（消融适用边界）；`labs/experiments/results-p9-2026-10-10.md` L83（N 与 GOMAXPROCS 关系）；`dnsproxi-go/LESSON.md` L85 | OK |
| 2. Green Tea GC 1.26 baseline | `$(go env GOROOT)/src/internal/buildcfg/exp.go:88`（`GreenTeaGC: true` 在 baseline 块） | 双跑实测（见 results-gc-2026-10-12.md） | `labs/experiments/results-gc-2026-10-12.md`（新）+ P6 L253 一句 | OK |
| 3. encoding/json/v2 实验性 | 默认构建 `import encoding/json/v2` 编译失败（实测报 "build constraints exclude all Go files"）；`GOEXPERIMENT=jsonv2` 构建运行成功 | bench 实测 | `notes/p19-ecosystem.md` L48（专段）+ `notes/p8-fast-track.md` L273（一句） | OK |

## 实测记录

### jsonv2（/tmp 临时工程，已删）

```
默认:      go build  → imports encoding/json/v2: build constraints
           exclude all Go files（v2 默认关闭实锤）
GOEXPERIMENT=jsonv2 go build && run → {"name":"hit"} -> {Name:hit At:0}
```

bench（benchtime=100000x，M4）:

| bench | v1（默认） | v2（jsonv2） |
|---|---|---|
| Marshal | 71.3 ns/op, 24 B/op, 1 alloc | 136.5 ns/op, 48 B/op, 2 allocs |
| Unmarshal | 296.8 ns/op, 248 B/op, 6 allocs | 147.1 ns/op, 24 B/op, 1 alloc |

结论：反序列化 2 倍速 + 分配 6→1；序列化反而慢（反射路径不同）。
API 未定型（GOEXPERIMENT 门控），生产别用。

### Green Tea GC 双跑

见 `labs/experiments/results-gc-2026-10-12.md`：三形态 min-of-5
区间交叠（差 0.1-1.8%，噪声内）。

### GOMAXPROCS cgroups

源码核实（非实测，本机 macOS 无 cgroup）：
- `runtime/cgroup_linux.go:13-20`：启动时读 `/proc/self/cgroup` 与
  `/proc/self/mountinfo` 找叶 cgroup 的 CPU limit 文件。
- `cgroup_linux_test.go:41-54`：`containermaxprocs=1` 用 cgroup 限额
  取 GOMAXPROCS；`=0` 忽略。
- 结论句落在 P8 消融（"macOS 测得，容器默认值可能显著低于核数"）与
  P9 建议（"N=8 是任务形态调参，与 GOMAXPROCS 独立生效"）。
