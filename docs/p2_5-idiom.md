---
title: P2.5 · 范式与惯用法：写出"像 Go"的代码
---

# P2.5 · 范式与惯用法：写出"像 Go"的代码


> 这章学完你能干什么：说清 Go 的三条设计哲学，按社区惯例设计接口、包装错误、组织包、写表驱动测试，让你的代码在 review 时不会被打回"这不 Go"。练习终点是把一段"外来户代码"重构成惯用形状，测试全绿。
> 读者：读完 P2/P3。前置：P1.5（语法）、P2（接口和错误基础）。后续：P8 起的工程章都按本章惯例写。
>
> 动手方式：开一个空目录，`go mod init demo`，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。

## 体系背景：包、gofmt 和测试二进制

Go 的很多惯例看似口味问题，背后其实是工具链的事实。先看三件。

第一，包是编译单元，目录就是包。`go list` 能直接列出它们：

```sh
$ go list -f '{{.ImportPath}} ({{.Name}})' ./clean ./internal/secret
idiomdemo/clean (clean)
idiomdemo/internal/secret (secret)
```

包名通常简短、小写，并与目录末段一致；实际包名由 `package` 声明决定，导入时也可以显式起别名。

第二，`gofmt` 是一个读语法树再整齐打印出来的程序，它没有配置项。拿一段乱缩进的代码试试：

```sh
$ gofmt -d ugly.go
-  x:=1
-	if x>0 {
-      fmt.Println( "ok" )
-  }
+	x := 1
+	if x > 0 {
+		fmt.Println("ok")
+	}
```

（输出省略了文件头。）全社区只有一种格式，没有可争论的空间。Java 的 `google-java-format` 和 JS 的 Prettier 做同样的事，但它们可以配置，Go 不行。

第三，测试不是在解释器里跑的。`go test -c` 把测试编译成一个独立的可执行文件：

```sh
$ go test -c -o clean.test ./clean
$ ./clean.test -test.v -test.run 'TestNormalize/empty'
=== RUN   TestNormalize
=== RUN   TestNormalize/empty
--- PASS: TestNormalize (0.00s)
    --- PASS: TestNormalize/empty (0.00s)
PASS
```

这就是为什么 Go 的测试不需要框架：它只是同一个包里的函数，加上一个由工具链生成的 `main`。

## 设计哲学

Go 的惯用法不是零散规则，是三条哲学的推论。

**直白优于聪明。** 没有宏、没有继承、没有隐式转换、没有操作符重载，读代码所见即所得。所以错误显式写 `if err != nil`，接口小到一眼看穿。

**组合优于继承。** 类型之间只有嵌入和接口满足。所以接口在使用方定义，结构体扁平。

**零值尽量可用。** `bytes.Buffer`、`sync.Mutex` 的零值可以直接用；需要不变量或依赖的类型仍可能要求构造函数，应以文档为准。

Rust 的哲学是编译期证明安全，Go 的是运行期简单可靠。Rust 把复杂度放进类型系统，Go 把复杂度放进约定和 code review。写 Go 时要把"编译器会抓"换成"约定要求我"。

## 使用方定义的接口

假设你写了一个函数，只想知道传进来的东西有多长。最直接的办法是去提供方的包里找一个接口。Go 的做法反过来：自己在函数旁边定义需要的那一个方法。

```go
// Lener 由使用方定义：我只需要一个 Len()。
type Lener interface{ Len() int }

func report(l Lener) { fmt.Println("长度", l.Len()) }

report(strings.NewReader("hello"))
report(bytes.NewBufferString("世界"))
```

```sh
$ go run main.go
长度 5
长度 6
```

`strings.Reader` 和 `bytes.Buffer` 的作者从没听说过 `Lener`，但它们都满足。使用方可以给任何第三方类型配接口，连标准库类型也不例外。

这和 Java、Kotlin 的习惯相反：接口通常由提供方声明，实现方显式写 `implements`。TypeScript 的结构化类型和 Go 最接近，Rust 的 trait 也定义在提供方。

## 小接口

小接口更容易被满足和组合，例如 `io.Reader`、`fmt.Stringer`。`sort.Interface` 仍可用于需要它的场景；对切片排序也可考虑 `slices.Sort` 或 `slices.SortFunc`。

小接口可以像积木一样拼。`io.ReadWriter` 就是把两个接口嵌在一起：

```sh
$ go doc io.ReadWriter
type ReadWriter interface {
	Reader
	Writer
}
    ReadWriter is the interface that groups the basic Read and Write methods.
```

用 `io.MultiReader` 把两个来源拼成一个，再交给 `io.Copy`，三个函数都只认 `Reader` 和 `Writer`：

```go
r := io.MultiReader(strings.NewReader("hello, "), strings.NewReader("世界\n"))
io.Copy(os.Stdout, r)
```

```sh
$ go run main.go
hello, 世界
```

Rust 的 trait 可以很大，`Iterator` 带几十个默认方法。Go 没有默认方法，大接口等于给实现者出难题。官方谚语是：接口越大，抽象越弱。

## 返回具体类型

一种常见建议是：参数只在需要多种实现时收接口，返回值在适合时返回具体类型。它不是强制规则；隐藏实现或表达多种结果时返回接口也有价值。

```go
func slurp(r io.Reader) (*bytes.Buffer, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(r)
	return &b, err
}

buf, _ := slurp(strings.NewReader("abc"))
fmt.Println(buf.Len(), buf.String())
```

```sh
$ go run main.go
3 abc
```

调用方可以直接用 `Len` 和 `String`，不用断言回具体类型。返回接口则能隐藏实现，但只暴露接口约定的方法；是否这样做取决于 API 的边界。`error` 本身就是很有用的接口返回值。

不要为了假设中的扩展而预先抽象；确需替身或隔离外部依赖时，使用方定义的小接口也有利于测试。`any`、泛型和具体类型各有适用场景。

## 错误链

P2 讲了 `%w` 和 `errors.Is`。这里补全链路上的惯例，先看每层加一句上下文后，最顶层读起来是什么样：

```go
var ErrNotFound = errors.New("not found")   // 包级错误变量，命名 ErrXxx

func readFile(path string) error { return fmt.Errorf("read %s: %w", path, ErrNotFound) }
func loadRules() error {
	if err := readFile("/etc/dns.yaml"); err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	return nil
}

err := loadRules()
fmt.Println(err)
fmt.Println(errors.Is(err, ErrNotFound), err == ErrNotFound)
```

```sh
$ go run main.go
load rules: read /etc/dns.yaml: not found
true false
```

每层写"这层在干什么"，顶层就是一条完整因果链。识别用 `errors.Is`，`==` 在包装后就失效了。

需要带字段的错误时，定义 `XxxError` 类型，用 `errors.As` 取回：

```go
type ParseError struct {
	Line int
	Msg  string
}
func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

err = fmt.Errorf("parse: %w", &ParseError{Line: 7, Msg: "bad token"})
var pe *ParseError
if errors.As(err, &pe) {
	fmt.Println("出错行", pe.Line)
}
```

```sh
出错行 7
```

`fmt.Errorf` 有两种动词，选哪个取决于你想不想让调用方继续识别：

```go
base := io.ErrUnexpectedEOF
w := fmt.Errorf("read config: %w", base)
v := fmt.Errorf("read config: %v", base)
fmt.Println(w, errors.Is(w, io.ErrUnexpectedEOF))
fmt.Println(v, errors.Is(v, io.ErrUnexpectedEOF))
```

```sh
read config: unexpected EOF true
read config: unexpected EOF false
```

`%w` 保留链路，`%v` 只留文字。对外 API 的边界处用 `%v` 翻译错误，避免内部错误泄漏成契约。

多个 goroutine 各自出错时，用 `errors.Join`（1.20 起）合并，`Is` 和 `As` 会穿透所有子错误：

```go
joined := errors.Join(ErrNotFound, io.EOF)
fmt.Println(joined)
fmt.Println(errors.Is(joined, ErrNotFound), errors.Is(joined, io.EOF))
```

```sh
not found
EOF
true true
```

P14 的 errgroup 只返回第一个错误，要收集全部就用 `Join` 或自己 `append`。

最后一条纪律：错误只处理一次，要么记日志，要么上抛，不同时做。同时做会怎样：

```go
func middle() error {
	err := lower()
	logs = append(logs, "ERROR: "+err.Error())   // 记了
	return fmt.Errorf("middle: %w", err)         // 又上抛
}
```

```sh
$ go run main.go
ERROR: disk full
ERROR: middle: disk full
```

一个故障打出两条日志，上层再记一次就是三条。这是 review 里被打回最多的点。

对照 Rust：`fmt.Errorf` 加 `%w` 相当于 `anyhow::Context`，自定义错误类型相当于 `thiserror`。

## 提前返回

错误路径立即 `return`，主逻辑保持零缩进，这叫 guard clause。对比 else 金字塔，行为完全一样：

```go
func pyramid(in string) string {
	if in == "" {
		return ""
	} else {
		if strings.Contains(in, "\n") {
			return ""
		} else {
			return strings.ToUpper(in)
		}
	}
}

func guard(in string) string {
	if in == "" || strings.Contains(in, "\n") {
		return ""
	}
	return strings.ToUpper(in)
}
```

```sh
$ go run main.go
"" "" ""
"a\nb" "" ""
"hello" "HELLO" "HELLO"
```

三行输入，两个版本的结果一模一样。`return` 之后的 `else` 是坏味道，linter 和 reviewer 都会指出。

## 命名

命名惯例可以用几条规则记住：

- 局部变量要短：`i`、`r`、`buf`，不写 `readerBuf`。
- 导出标识用 PascalCase，未导出用 camelCase。缩写只用公认的，比如 `ParseIP`。
- getter 不加 `Get`：写 `c.Count()`，不写 `c.GetCount()`。setter 才用 `SetCount`。
- 单方法接口用"动词加 er"：`Reader`、`Closer`、`Fetcher`。
- 错误变量叫 `ErrXxx`，错误类型叫 `XxxError`。
- 接收者用类型首字母小写，如 `func (c *Counter)`，不用 `this` 或 `self`，同一类型的各方法保持同名。

Java 读者要改掉的习惯是 `getX()`。Kotlin 的属性和 JS 的 `get x()` 都没有这个前缀，Go 的约定和它们一致。

## 包与 internal

`main` 包只做装配：读配置、连依赖、起服务，业务全在库包里。包名小写、单个单词、不复数，避免 `util` 和 `common` 这类垃圾抽屉，按领域拆。

`internal/` 由 Go 工具链强制限制导入：只有其父目录树内的代码能导入，不是简单地按模块划界。下面的同模块例子恰好也在允许范围内：

```sh
$ go run ./useint
42
```

换另一个模块去导入同一个包：

```sh
$ go run main.go
package command-line-arguments
	main.go:6:2: use of internal package idiomdemo/internal/secret not allowed
```

对应的是 Java 的 `module-info.java` 里不 `exports` 的包，或 npm `package.json` 的 `exports` 字段。

`pkg/` 目录在社区已经式微。2026 年的主流做法是根包加 `internal`，除非你真的要对外提供库。

## 文档注释

导出标识的注释以标识符名字开头，全文即文档，`go doc` 直接渲染：

```go
// Normalize collapses runs of whitespace into single spaces and trims the ends.
// It returns an empty string for blank input.
func Normalize(s string) string { ... }
```

```sh
$ go doc ./clean Normalize
func Normalize(s string) string
    Normalize collapses runs of whitespace into single spaces and trims the
    ends. It returns an empty string for blank input.
```

规则和 Rust 的 `///`、Javadoc、TSDoc 几乎一样，Go 多一条"必须以名字开头"。

## 表驱动测试

Go 的测试惯例是同包的 `xxx_test.go`。要测黑盒就用 `xxx_test` 包，标准库 `net/http` 自己就这么做。

标准形状是表驱动加 `t.Run`：

```go
tests := []struct {
	name string
	in   string
	want string
}{
	{"already clean", "a b", "a b"},
	{"mixed spaces", "  a   b ", "a b"},
	{"tabs and newlines", "a\t\nb", "a b"},
	{"empty", "", ""},
}
for _, tt := range tests {
	t.Run(tt.name, func(t *testing.T) {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	})
}
```

```sh
$ go test -v ./clean
=== RUN   TestNormalize
=== RUN   TestNormalize/already_clean
=== RUN   TestNormalize/mixed_spaces
=== RUN   TestNormalize/tabs_and_newlines
=== RUN   TestNormalize/empty
--- PASS: TestNormalize (0.00s)
    --- PASS: TestNormalize/already_clean (0.00s)
    --- PASS: TestNormalize/mixed_spaces (0.00s)
    --- PASS: TestNormalize/tabs_and_newlines (0.00s)
    --- PASS: TestNormalize/empty (0.00s)
PASS
```

子测试名里的空格被 `t.Run` 换成了下划线。只跑某一个用例，用 `/` 选子测试：

```sh
$ go test -v -run 'TestNormalize/empty' ./clean
=== RUN   TestNormalize
=== RUN   TestNormalize/empty
--- PASS: TestNormalize (0.00s)
    --- PASS: TestNormalize/empty (0.00s)
```

断言用 `t.Errorf` 和 `t.Fatalf`。社区不用 assert 库，Go 觉得 `if` 就够。需要并行时加 `t.Parallel()`，临时目录用 `t.TempDir()`。

对应到其他语言，表驱动相当于 JUnit 的 `@ParameterizedTest` 和 Jest 的 `test.each`。

## 小结

1. 三条哲学：直白优于聪明、组合优于继承、零值可用。所有惯例都能追溯到它们。
2. 按使用方的实际需求定义接口；参数和返回值选择接口还是具体类型，要看 API 边界。
3. 错误链：`ErrXxx` 加 `XxxError`，用 `%w` 包装，用 `Is` 和 `As` 识别，只处理一次。
4. 提前返回，不留 else。getter 不加 `Get`，包名不要 util。
5. 注释以名字开头，测试用表驱动加 `t.Run`，断言靠 `if`。

## 练习：p2_5idiom

练习在 `exercises/p2_5idiom/`。`legacy.go` 预置了一段能跑但不地道的代码，`idiom.go` 是重构后的参考实现。先看全绿长什么样：

```sh
$ go test ./exercises/p2_5idiom/
ok  	golearn/exercises/p2_5idiom	0.464s
```

五个练习对应本章五个惯例：

- `TestInterfaceDesign`：七方法胖接口挪到使用方，拆成两个单方法接口。
- `TestErrorChain`：裸 `errors.New` 加 `==` 比较，改成 `%w` 包装链加 `errors.Is`。
- `TestGuardClause`：else 金字塔改成提前返回。
- `TestGetter`：`GetCount()` 改名 `Count()`。
- `TestTableDriven`：复制粘贴的测试合并成表驱动。

**预测**，先写答案再跑：

1. `legacy.go` 里 `SentinelCompare(ErrNotFound)` 返回什么？`SentinelCompare(fmt.Errorf("x: %w", ErrNotFound))` 呢？这说明为什么要用 `errors.Is`？
2. `TestTableDriven` 加 `-v` 时，子测试名长什么样？`"mixed spaces"` 里的空格会变成什么？

第 1 题实测是 `true` 和 `false`，`==` 只认没有包装的原值。第 2 题的答案见上一节的输出。

**观察**：

```sh
go test -v -run TestTableDriven ./exercises/p2_5idiom/            # 看 TestTableDriven/mixed_spaces 这样的子测试名
go test -v -run 'TestTableDriven/empty' ./exercises/p2_5idiom/    # 只跑一个子用例
```

体会表驱动的定位成本：失败时子测试名直接告诉你是哪一行。

**改造**：给 `Fetcher` 接口加一个内存假实现（fake），不用 mock 框架。参考答案是 `TestFake` 里的 `fakeReader`。体会"Go 测试靠手写 fake，接口小所以 fake 也小"。

## 章末参考

### Effective Go 和 Code Review Comments 速查

- `gofmt` 无配置，全社区一种格式。
- `var _ Interface = (*T)(nil)` 做编译期实现断言（P2 参考节详述）。
- `init()` 少用，隐式初始化难追踪，测试里用 TestMain。
- iota 枚举配 `stringer`，用 `go generate` 生成 `String()`（P19）。
- 结构体标签是反引号字符串，json、yaml、db 都靠它。
- 库代码不要 panic，返回 error，panic 留给 bug（P1.5 的 panic 小节）。
- map 并发读写会 panic，用锁或分片（P3）。
- context 永远是第一个参数（P3）。
- 数字字面量可以写 `1_000_000`（1.13 起）。
- 时间处理用 `time` 包，别用 unix 秒裸算，`time.Duration` 贯穿全程。

### 模块和工具纪律

`go mod tidy` 根据导入和测试整理模块依赖（P1），`go.mod` 参与版本选择，`go.sum` 保存校验哈希。升级用 `go get -u ./...` 或逐包。主版本 2 及以上通常在路径末尾带 `/v2`，这是 Semantic Import Versioning。

`gopls` 驱动重命名、引包和跳转，保存即 `gofmt`。lint 交给 `golangci-lint`，启用集的配置在 P1 章末参考。

### Go Proverbs（Rob Pike）精选

- Clear is better than clever.
- A little copying is better than a little dependency.
- Interface{} says nothing.
- The bigger the interface, the weaker the abstraction.
- Don't communicate by sharing memory, share memory by communicating.
- Errors are values.
- Don't just check errors, handle them gracefully.

每条都能在 P2、P2.5、P3 找到对应展开。

### Rust 到 Go 的惯用法映射

| Rust 惯用法 | Go 对应 | 差异要点 |
|---|---|---|
| `impl Trait for T` | 方法加隐式满足 | 接口在使用方 |
| `?` 传播 | `if err != nil { return %w }` | 手写，显式 |
| `thiserror` | 自定义错误类型 | 手动实现 `Error()` |
| `anyhow::Context` | `fmt.Errorf("...: %w", err)` | 同构 |
| `#[derive(Debug)]` | `fmt.Stringer` 或 `%+v` | `fmt` 自带结构体打印 |
| builder pattern | functional options（P19 详述） | 可变参数配置惯例 |
| `Default::default()` | 零值加 `NewXxx` | 零值可用哲学 |
| cargo feature | build tags 或独立包 | 无条件编译宏 |
| `mod tests` | 同目录 `_test.go` | 白盒黑盒按包名后缀 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| 使用方定义接口 | 提供方声明，显式 `implements` | 结构化类型，最接近 |
| 小接口 | 函数式接口，如 `Runnable` | 函数类型 |
| `error` 加 `%w` | `new Exception(msg, cause)` | `new Error(msg, { cause })` |
| `errors.Join` | `addSuppressed` | `AggregateError` |
| `internal/` | `module-info.java` 不 `exports` | `package.json` 的 `exports` |
| 无配置 `gofmt` | `google-java-format`，可配 | Prettier，可配 |
| 表驱动测试 | `@ParameterizedTest` | `test.each` |
| getter 不加 `Get` | `getX()` 约定 | `get x()` 或属性 |
