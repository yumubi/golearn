// Package p30iface：P30 方法、接口与组合的练习锚点。
//
// 全部测试与本章正文一一对应；Rust 对照写在各考点注释里。
// 约束：不写完整 JSON 库（TestHandwrittenJSON 是两字段小用例），
// 注入用 defer 版 trace（不依赖 AST 工具本体）。
package p30iface

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"unicode"
)

// ===== 共用类型：方法表达式与 receiver 等价变换（正文 §2）=====

type T struct{ n int }

func (t T) Get() int   { return t.n } // 等价于 func Get(t T) int
func (t *T) Set(a int) { t.n = a }    // 等价于 func Set(t *T, a int) int
func F1(t T) int       { return t.n } // Get 的机械改写
func F2(t *T, a int)   { t.n = a }    // Set 的机械改写

// ===== 共用类型：方法集合矩阵（正文 §3）=====

type Iface interface {
	M1()
	M2()
}

type Impl struct{}

func (Impl) M1()  {} // 值 receiver：进 T 和 *T 的方法集合
func (*Impl) M2() {} // 指针 receiver：只进 *T 的方法集合

// 编译期断言（正文 §3 的 var _ Iface = ... 矩阵，值格在测试里用注释矩阵验证）。
var _ Iface = (*Impl)(nil)

// dumpMethodSet 返回 v 的（动态类型上的）方法名集合，正文 §3 判定表的可视化。
// 指针实参保留指针类型，直接读 *T 的方法集合（Elem 后会丢掉指针方法）。
func dumpMethodSet(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumMethod())
	for i := range t.NumMethod() {
		names = append(names, t.Method(i).Name)
	}
	return names
}

// ===== 共用类型：嵌入（正文 §4）=====

type T1 struct{}

func (T1) M1()  {}
func (*T1) M2() {}

type T2 struct{}

func (T2) M3()  {}
func (*T2) M4() {}

type S struct {
	T1
	*T2
}

// 别名与新类型的方法集合对照（TestTypeAliasMethodSet）。
type Alias = T
type Defined T // defined 新类型：方法集合为空

// ===== 共用类型：接口运行时（正文 §6）=====

type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

// failTrap 返回被装箱的 *MyErr 类型 nil 指针（接口字非零、data 为 nil）。
func failTrap() error {
	var p *MyErr // nil
	return p     // 装箱：tab != nil，data == nil
}

// ===== 共用类型：Save 与包装链（正文 §7/§8）=====

// Save 只依赖唯一用到的能力：io.Writer。
func Save(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

// CapReader 是包装器：Read 后把已读部分转大写（正文 §8 的链式单元）。
type CapReader struct{ r io.Reader }

func (c *CapReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for i := range n {
		p[i] = byte(unicode.ToUpper(rune(p[i])))
	}
	return n, err
}

// NewCapReader 返回接口类型：包装器签名 func(I) I，可链式。
func NewCapReader(r io.Reader) io.Reader { return &CapReader{r: r} }

// CountReader 统计 Read 次数，链的最外层观察者。
type CountReader struct {
	r     io.Reader
	Count int
}

func (c *CountReader) Read(p []byte) (int, error) {
	c.Count++
	return c.r.Read(p)
}

// ===== 共用类型：中间件（正文 §8）=====

type mwFunc func(http.Handler) http.Handler

func logHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Saw-Log", "1") // 记录：日志层确实跑过
		h.ServeHTTP(w, r)
	})
}

func authHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("auth") != "123456" {
			http.Error(w, "bad auth param", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ===== 共用类型：反射 API（正文 §9）=====

type User struct {
	Name string `json:"name"`
	addr string // 未导出：CanSet 为 false
}

func Add(a, b int) int { return a + b }

// ===== 共用类型：trace（正文 §9）=====
// Trace 是正文 defer 版最简跟踪：注册时打印 enter，返回的闭包在 defer LIFO 里打印 exit。
// 生产禁用（gid/缩进版本见 TB27 单元），这里只做输出契约。

// out 是 Trace 的输出目的地，测试里重定向到 buffer 断言输出契约。
var out io.Writer = os.Stdout

func Trace(name string) func() {
	fmt.Fprintln(out, "enter:", name)
	return func() { fmt.Fprintln(out, "exit:", name) }
}

// traceInner/traceOuter 是手动注入 defer Trace 的样例函数（AST 注入后的形态）。
func traceInner() {
	defer Trace("inner")()
	fmt.Fprint(out, "inner-")
}

func traceOuter() {
	defer Trace("outer")()
	traceInner()
	fmt.Fprint(out, "outer-")
}

// _ 保持 bufio 引用（CapReader 链的 io.Reader 源在测试里用 strings.Reader，bufio 留给改造题）。
var _ = bufio.NewReader
