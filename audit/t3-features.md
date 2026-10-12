# T3 审计：Go 1.21 到 1.26 新特性覆盖

> PLAN-OMP.md T3 产出。每个特性：`go doc`/源码核实签名与行为 →
> 落点章 + 可编译例子（配测试，全部 `-race` 跑过）→ Rust 对照。
> 核实基准：go1.26.4 darwin/arm64（`go version` 实测）。

## 特性总表

| 特性 | 版本 | 落点章 | 例子文件（测试名） | 状态 |
|---|---|---|---|---|
| 内建 `min`/`max`/`clear` | 1.21 | notes/p1_5-basics.md（章末参考"内建 min/max/clear"） | `exercises/p1_5basics/builtin121.go`（TestMinMaxBuiltins/TestMinMaxInts/TestClearBuiltins） | OK |
| `errors.AsType[E]` | 1.26 | notes/p2-lang-core.md L287（错误处理补充） | `exercises/p2lang/astype.go`（TestAsTypeVsAs） | OK |
| `sync.WaitGroup.Go` | 1.25 | notes/p3-concurrency.md L244（共享状态决策树补注）；P8 原有 | `exercises/p3conc/wggo.go`（TestWaitGroupGo） | OK |
| `strings.SplitSeq` | 1.24 | notes/p8-fast-track.md L110（迭代器段） | `exercises/p8fast/modern_std_test.go`（TestSplitSeq） | OK |
| `testing.B.Loop` | 1.24 | notes/p8-fast-track.md L193（bench 段，原有） | `exercises/p8fast/modern_std_test.go`（BenchmarkFieldLensLoop） | OK |
| json 标签 `omitzero` | 1.24 | notes/p8-fast-track.md L256（JSON 段） | `exercises/p8fast/modern_std_test.go`（TestOmitZero） | OK |
| `os.Root` | 1.24 | notes/p20-cli-tool.md L150（文件三件套后） | `exercises/p20cli/root.go`（TestRootContains/TestRootEscape） | OK |

## 逐条核实记录

| # | 核对方式 | 命令或出处 | 结果 |
|---|---|---|---|
| 1 | go doc | `go doc builtin.min` | `func min[T cmp.Ordered](x T, y ...T) T`，NaN 语义见 doc |
| 2 | go doc | `go doc builtin.clear` | `func clear[T ~[]Type \| ~map[Type]Type1](t T)`：map 全删、slice 元素归零 |
| 3 | 重跑 | `go test -race -count=1 ./exercises/p1_5basics/` | ok（3 个新测试全绿）；min 不收 `...xs` 是实测坑（`invalid use of ... with built-in min`） |
| 4 | go doc | `go doc errors.AsType` | `func AsType[E error](err error) (E, bool)`，深度优先穿 Unwrap 树 |
| 5 | 重跑 | `go test -race -count=1 ./exercises/p2lang/` | ok；AsType 与 As 对同一 `%w` 链取回同值 |
| 6 | go doc | `go doc sync.WaitGroup.Go` | `func (wg *WaitGroup) Go(f func())`：`go f` + Add + Done 三合一 |
| 7 | 重跑 | `go test -race -count=1 ./exercises/p3conc/` | ok（TestWaitGroupGo 并发平方和正确） |
| 8 | go doc | `go doc strings.SplitSeq` | `func SplitSeq(s, sep string) iter.Seq[string]`，单次迭代器，值序同 Split |
| 9 | 重跑 | `go test -race -count=1 -run TestSplitSeq ./exercises/p8fast/` | ok（含早停分支） |
| 10 | go doc | `go doc testing.B.Loop` | `func (b *B) Loop() bool`；防折叠语义见 doc 引 Go 1.24 release note |
| 11 | 重跑 | `go test -run xxx -bench FieldLensLoop -benchtime 10x ./exercises/p8fast/` | `BenchmarkFieldLensLoop-10  10  95.80 ns/op` 实拍 |
| 12 | 源码 | `$(go env GOROOT)/src/encoding/json/encode.go:136` | "The omitzero option specifies that the field should be omitted..."；认 `IsZero() bool` 接口 |
| 13 | 重跑 | `go test -race -count=1 -run TestOmitZero ./exercises/p8fast/` | ok；零值 struct 整体省略（曾因标签误写 `omitemptyzero` 红，改 `omitzero` 后绿） |
| 14 | go doc | `go doc os.Root` | "Root may be used to only access files within a single directory tree" |
| 15 | 重跑 | `go test -race -count=1 ./exercises/p20cli/` | ok；`..` 逃逸与绝对路径均被拒（TestRootEscape） |

## Rust 对照（每特性一段）

- `min`/`max`：Rust `Ord::min/max` 方法（`a.min(b)`）；Go 是内建函数
  非方法，且不收变参切片。
- `clear`：Rust `Vec::clear`/`HashMap::clear` 方法；Go 内建函数，
  slice 语义不同（归零不缩容，`len` 不变）。
- `errors.AsType`：Rust `downcast_ref::<T>()`；Go 版穿透 `Unwrap`
  链且返回 `(E, bool)` 而非 `Option<&T>`（值拷贝出具体类型）。
- `WaitGroup.Go`：Rust `JoinSet::spawn`（任务自动入集合，`join_all`
  收口）；Go 保留 Wait/计数器心智模型。
- `SplitSeq`：Rust `str::split` 返回 `Split<'_>` 惰性迭代器；Go 1.24
  才补上（此前 `Split` 一次建切片）。
- `B.Loop`：Rust `#[bench]` 已出局，生态用 criterion 的 `iter()`；
  `b.Loop` 是标准库内建版，防折叠语义等价 `iter::black_box` 保护。
- `omitzero`：Rust serde `skip_serializing_if` + `is_zero` 谓词；
  Go 版只认 `IsZero() bool` 接口（自定义谓词不行）。
- `os.Root`：Rust 无内建对应，惯例是 `Components` 逐段过滤 +
  `canonicalize`；Go 版下沉到 openat 系统调用层，防 TOCTOU 更彻底。

## 边界说明（验收者关注点）

- 例子全部随包测试跑（`go test -race ./exercises/...` 绿），不是
  独立 demo；bench 例用 `-benchtime 10x` 快跑取证。
- `errors.AsType` 在 P2 是"章末参考"级补充（PLAN T3 指定的
  P2/P2.5 错误链处）；P2.5 的 `errors.As` 主线示例保留不动。
- P8 的 `Item` 示意 struct 加了 `Meta Meta` 字段仅为展示标签写法，
  该代码块本身不编译（正文示意）；可编译版本在 TestOmitZero。
