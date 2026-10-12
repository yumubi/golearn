# P2.5 · 范式与惯用法：写出"像 Go"的代码

> 语法对了（P1.5），迁移误判避开了（P2），下一关是**惯用法**：同样
> 能跑的代码，Go 老手一眼分得出"外来户写法"。这章收集最高频的范式
> 约定：接口怎么设计、错误怎么包、context 怎么传、包怎么组织、
> 命名怎么起。读完你写的代码 review 时不会被打回"这不 Go"。
>
> 读者：读完 P2/P3。前置：P1.5（语法）、P2（接口/错误基础）。
> 后续：P8 起（工程章）全部按本章惯例写。

## 这章学完你能干什么

- 说清 Go 的三条设计哲学，并用它们解释具体惯用法为什么存在。
- 按社区惯例设计接口（使用方定义、小接口、返回具体类型）。
- 包结构、命名、注释按 `gofmt` + Effective Go + Go Code Review
  Comments 的标准走。
- 练习终点：`exercises/p2_5idiom/` 把一段"外来户代码"重构成惯用
  形状，测试全绿。

五个问题：

1. Go 的设计哲学是哪三条，怎么从它们推出所有惯例？
2. 接口设计的黄金法则是什么？
3. 错误处理的完整惯例链（sentinel/包装/As/Join）？
4. 包怎么组织、命名怎么起才算地道？
5. 代码之外的惯例：godoc、测试、模块纪律？

## 问题 1：三条设计哲学，推出一切惯例

Go 的惯用法不是零散规则，是三条哲学的推论：

**① 直白优于聪明**（clear is better than clever）。没有魔法：没有
宏、没有继承、没有隐式转换、没有操作符重载。读代码所见即所得。
→ 推出：错误显式 if err != nil（不用异常跳转）；并发用 channel
传递数据所有权而不是共享；接口小到一眼看穿。

**② 组合优于继承**。类型之间只有嵌入（委托糖）和接口满足，没有
父类。→ 推出：接口在使用方定义；结构体扁平；"继承树"用接口 +
嵌入拼装。

**③ 零值可用**。`var x T` 出来就该能用，构造函数是补救不是必需。
→ 推出：`bytes.Buffer`/`sync.Mutex`/`time.Time{}` 零值直接用；
库 API 设计成 `NewXxx` 只在零值不够时提供。

Rust 对照：Rust 的哲学是"编译期证明安全"，Go 的是"运行期简单可靠"
：所以 Rust 把复杂度放进类型系统，Go 把复杂度放进约定和 code
review。**写 Go 时把"编译器会抓"换成"约定要求我"的心理准备。**

## 问题 2：接口设计的黄金法则

三条，从强到弱：

**① 接口定义在使用方**。谁用谁定义，不管提供方：

```go
// package storage（使用方）自己定义需要的能力，
// 而不是 import 提供方包里的接口
type Fetcher interface {
    Fetch(ctx context.Context, key string) ([]byte, error)
}
```

Rust 反着来：trait 定义在提供方（谁实现谁声明）。Go 这么设计的
收益：使用方可以给任何第三方类型配接口（连标准库类型都能满足你
的接口），无需改对方代码。

**② 越小越好**。单方法接口是常态：

```go
io.Reader    // Read 一发
fmt.Stringer // String 一发
sort.Interface // 三个（Len/Less/Swap 已被 slices.Sort 取代）
```

小接口才能被积木式组合：`io.ReadWriter = io.Reader + io.Writer`
（接口嵌接口）。Rust 对照：trait 可以很大（`Iterator` 几十个方法
带默认实现），Go 没有默认方法，大接口 = 实现者地狱。

**③ 返回具体类型，参数收接口**。函数签名"进松出严"：

```go
// 参数收 io.Reader（任何可读源），返回 *bytes.Buffer（具体类型）
func ReadAll(r io.Reader) (*bytes.Buffer, error)
```

返回接口把调用方的选择权锁死（想用具体方法还得断言回去）。
例外：`error` 本身是接口：历史包袱，全生态已约定俗成。

**anti-pattern 清单**：`interface{}` 当万能参数（用泛型或具体类型）；
"为了 Mock 而先抽接口"（Go 的惯例是给真实类型写测试，需要时再抽，
见问题 5）；接口定义在实现文件里没人用（Go 官方谚语：
The bigger the interface, the weaker the abstraction）。

## 问题 3：错误处理的完整惯例链

P2 问题 3 讲了 `if err != nil` 和 `%w` 包装。这里补全链路的惯例：

**sentinel error（包级错误变量）**：

```go
var ErrNotFound = errors.New("not found")   // 命名规范 ErrXxx

// 调用方用 errors.Is 识别（不是 ==，包装链会断）
if errors.Is(err, ErrNotFound) { ... }
```

**错误类型（带上下文）**：

```go
type ParseError struct {
    Line int
    Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// 调用方用 errors.As 取回具体类型
var pe *ParseError
if errors.As(err, &pe) { fmt.Println(pe.Line) }
```

**包装与上下文**（fmt.Errorf 三种姿势）：

```go
fmt.Errorf("read config: %w", err)      // %w 包装：链路保留，Is/As 可穿透
fmt.Errorf("read config: %v", err)      // 不包装：边界处翻译错误（对外 API）
fmt.Errorf("read %s: %w", path, err)    // 最常见：加上下文 + 透传
```

惯例：**每层加一句"这层在干什么"**，最顶层 error 读起来是完整因果
链：`load rules: read /etc/dns.yaml: permission denied`。
对照 Rust 的 `thiserror`（自动 impl Error）和 `anyhow`（带上下文的
动态错误）：Go 的 `fmt.Errorf+%w` = anyhow，自定义类型 = thiserror。

**重复错误的处理纪律**：只处理一次 = 要么记日志要么上抛，**不同时
都做**（日志重复是 review 高频打回点）。

**errors.Join（1.20+）**：多个 goroutine 各自出错时合并，
`errors.Is/As` 穿透全部子错误（P14 的 errgroup 返回的就是首个错，
要收集全部用 Join 或自己 append）。

## 问题 4：包组织与命名惯例

**包结构**（对照 P1 的目录树，这里说惯例）：

- `main` 包只做装配（读配置、连依赖、起服务），业务全在库包里。
- `internal/` 放不想暴露的实现；`pkg/` 在社区已式微（2026 主流
  直接用根包 + internal，除非真的要对外供库）。
- 包名**小写、单词、不复数**：`net/http` 不叫 `net/https`；
  `strconv` 不叫 `stringconv`。导入路径最后一段 = 包名（main 除外）。
- 避免 `util`/`common`/`misc`：垃圾抽屉包名是坏味道，按领域拆。

**命名**（Effective Go + Go Code Review Comments 精华）：

| 场景 | 惯例 | 例子 |
|---|---|---|
| 局部变量 | 短 | `i`、`r`（reader）、`buf`，不用 `readerBuf` |
| 导出标识 | PascalCase，不缩写除非公认 | `Client`、`ParseIP`（IP 公认） |
| 未导出 | camelCase | `newFetcher` |
| getter | **不加 Get** | `c.N()` 不叫 `c.GetN()` |
| setter | `SetXxx` | `c.SetN(3)` |
| 接口 | 单方法 = 动词 + er | `Reader`、`Closer`、`Fetcher` |
| 错误变量 | `ErrXxx` | `ErrNotFound` |
| 错误类型 | `XxxError` | `ParseError` |
| 缩进块内 return 后不再 else | if 提前返回 | 见下 |

**if 提前返回**（guard clause）是 Go 错误处理的形状：

```go
// 惯用：错误路径立即 return，主逻辑零缩进
if err != nil {
    return fmt.Errorf("open: %w", err)
}
return process(f)

// 外来户：else 套 else，金字塔：review 必打回
if err != nil {
    return err
} else {
    return process(f)   // 这个 else 是坏味道
}
```

**接收者命名**：类型首字母小写（`func (c *Counter)`），不用 `this`
/`self`，不同方法保持同名。

## 问题 5：代码之外的惯例

**godoc 注释**：导出标识的注释以标识符名开头，全文即文档：

```go
// Load parses the YAML config at path and validates required fields.
// It returns ErrNotFound if the file does not exist.
func Load(path string) (*Config, error) { ... }
```

`go doc ./pkg` / pkg.go.dev 直接渲染。对照 Rust 的 `///` doc comment，
规则几乎一样，Go 多一条"必须以名字开头"。

**测试惯例**（P8 问题 3 展开工具链，这里说形状）：

- 文件 `xxx_test.go` 与源码同包（白盒）或 `xxx_test` 包（黑盒，
  `package http_test`：标准库 http 自己就这么干）。
- table-driven 是标准姿势：`tests := []struct{ name string; ... }`
  + `t.Run(name, func(t *testing.T){...})`。
- 断言用 `t.Errorf/t.Fatalf`，**社区不用 assert 库**（对照 Rust 的
  `assert_eq!`，Go 觉得 if 就够）。
- 并行子测试 `t.Parallel()`；临时资源 `t.TempDir()`/`defer cleanup`。

**模块纪律**：`go mod tidy` 是唯一真相（P1）；版本升级
`go get -u ./...` 或逐包；主版本 2+ 路径带 `/v2`（Sematic Import
Versioning）：对照 cargo 的版本号自动解析，Go 的 v2 路径是显式
的，很多人第一次 import 报 404 就栽在这。

**重构时代的 IDE 纪律**：`gopls`（官方 LSP）驱动 rename/引包/
跳转；保存即 `gofmt`（无配置）；lint 交给 `golangci-lint`（P1 提过，
P19 讲配置）。

## 动手：练习 p2_5idiom（30-45 分钟）

```sh
go test ./exercises/p2_5idiom/ -v
```

**预测题**（先写答案再跑）：

1. `legacy.go` 的 `SentinelCompare(ErrNotFound)` 返回什么？
   `SentinelCompare(fmt.Errorf("x: %w", ErrNotFound))` 呢？这说明
   为什么要用 `errors.Is`？（实测：`true`、`false`，`==` 只认未包装的原值）
2. `TestTableDriven` 跑 `-v` 时，子测试名长什么样？
   `"mixed spaces"` 里的空格会变成什么？（t.Run 把空格改成 `_`）

这个包预置了一段"能跑但不地道"的代码（`legacy.go`），你的任务按
注释提示重构：

1. `TestInterfaceDesign`：接口定义在实现方 + 七方法胖接口 → 挪到
   使用方 + 拆成两个单方法接口，实现类型不变。
2. `TestErrorChain`：裸 `errors.New` 丢上下文 + `==` 比较 → 加
   `%w` 包装链，改 `errors.Is`，断言错误消息含完整因果。
3. `TestGuardClause`：else 金字塔 → 提前返回，主逻辑零缩进。
4. `TestGetter`：`GetCount()` → 改名 `Count()`，调用方同步改。
5. `TestTableDriven`：一坨复制粘贴的测试函数 → 合并成 table-driven。

**观察题**：`go test -v -run TestTableDriven ./exercises/p2_5idiom/`
看 `TestTableDriven/mixed_spaces` 这样的子测试名；再用
`-run 'TestTableDriven/empty'` 只跑一个子用例，体会 table-driven 的
定位成本。

**改造题**（参考答案是 `TestFake` 里的 `fakeReader`）：给 `Fetcher` 接口加一个内存假实现（fake），不用 mock
框架：体会"Go 测试靠手写 fake，接口小所以 fake 也小"。

## Take-away

1. 三条哲学：直白优于聪明、组合优于继承、零值可用：所有惯例都
   能追溯到它们。
2. 接口：使用方定义、单方法、返回具体类型参数收接口。
3. 错误链：sentinel（ErrXxx）+ 类型（XxxError）+ `%w` 包装 +
   Is/As 识别；错误只处理一次。
4. 包名小写不复数、拒绝 util 垃圾抽屉；getter 不加 Get；if 提前
   返回不留 else。
5. 注释以名字开头（godoc）；测试 table-driven + t.Run，断言靠 if
   不靠 assert 库。

---

## 章末参考

### Effective Go / Code Review Comments 高频条目速查

| 条目 | 一句话 |
|---|---|
| gofmt 无配置 | 全社区一种格式，别开方子 |
| `var _ Interface = (*T)(nil)` | 编译期断言实现（P2 参考节详述） |
| init() 少用 | 隐式初始化难追踪；测试里用 TestMain |
| iota 枚举配 stringer | `go generate` 生成 String()（P19） |
| 结构体标签 tag: 值 | 反引号字符串，json/yaml/db 都靠它 |
| 不要 panic | 库代码返回 error；panic 留给 bug（P1.5 §9） |
| copy-on-write 的 map | 并发读写 panic，锁或分片（P3 决策树） |
| context 第一参数 | `func Do(ctx context.Context, ...)`（P3 纪律） |
| 数字字面量下划线 | `1_000_000`（1.13+）可读性 |
| time 处理用 time 包 | 别用 unix 秒裸算；`time.Duration` 贯穿 |

### Go Proverbs（Rob Pike）精选

- Clear is better than clever.
- A little copying is better than a little dependency.
- Interface{} says nothing.
- The bigger the interface, the weaker the abstraction.
- Don't communicate by sharing memory, share memory by communicating.
- Errors are values.
- Don't just check errors, handle them gracefully.

每条都能在 P2/P2.5/P3 找到对应展开，面试和 review 都用得上。

### Rust → Go 惯用法映射表

| Rust 惯用法 | Go 对应 | 差异要点 |
|---|---|---|
| `impl Trait for T` | 方法 + 隐式满足 | 接口在使用方 |
| `?` 传播 | `if err != nil { return %w }` | 手写，显式 |
| `thiserror` | 自定义错误类型 | 手动实现 Error() |
| `anyhow::Context` | `fmt.Errorf("...: %w", err)` | 同构 |
| `#[derive(Debug)]` | `fmt.Stringer` 或 `%+v` | 结构体打印 fmt 自带 |
| builder pattern | functional options（P19 详述） | Go 的可变参数配置惯例 |
| `Default::default()` | 零值 + `NewXxx` | 零值可用哲学 |
| cargo feature | build tags / 独立包 | 无条件编译宏 |
| `mod tests` | 同目录 `_test.go` | 白盒黑盒按包名后缀 |
