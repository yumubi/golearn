// Package p30iface：P30 的 10+1 个测试，全部对应正文小节。
// 预测题（先写答案再跑）标 PREDICT；观察题标 WATCH；改造题标 MODIFY。
package p30iface

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestReceiverAsFirstParam：方法 = 首 receiver 参数的函数。
// 预测：Get(t) 与 F1(t)、Set(&t,a) 与 F2(&t,a) 行为完全一致；%T 打印方法表达式类型。
// Rust 对照: T::get 与 Type::method 路径语法几乎一致。
func TestReceiverAsFirstParam(t *testing.T) {
	var tv T
	tv.Set(3)
	F2(&tv, 5)
	if got, want := tv.Get(), F1(tv); got != want {
		t.Fatalf("Get=%d, F1=%d", got, want)
	}
	if n := tv.n; n != 5 {
		t.Fatalf("F2 没改到原实例: n=%d", n)
	}
	f1 := (*T).Set // func(*T, int)
	f2 := T.Get    // func(T) int
	if reflect.TypeOf(f1).String() != "func(*p30iface.T, int)" {
		t.Fatalf("(*T).Set 类型: %s", reflect.TypeOf(f1))
	}
	if reflect.TypeOf(f2).String() != "func(p30iface.T) int" {
		t.Fatalf("T.Get 类型: %s", reflect.TypeOf(f2))
	}
}

// TestMethodSetMatrix：T/*T × 接口赋值四格矩阵。
// 预测：只有 *T 赋值那两格编译通过；T 格因 M2 缺席被排除。
// 考点：dumpMethodSet(T) = [M1]，dumpMethodSet(*T) = [M1 M2]。
// 两个方向都实测过：M2 改值接收者后 T/*T 方法集合同为 {M1,M2}，四格全过；
// M1 改指针接收者后 T 只剩 M2，Impl{} 那格重新挂（正文 §方法集合 动手）。
func TestMethodSetMatrix(t *testing.T) {
	vs, ps := dumpMethodSet(Impl{}), dumpMethodSet(&Impl{})
	if len(vs) != 1 || vs[0] != "M1" {
		t.Fatalf("T 方法集合 = %v, 只要 M1", vs)
	}
	if len(ps) != 2 || ps[0] != "M1" || ps[1] != "M2" {
		t.Fatalf("*T 方法集合 = %v, 要 M1 M2", ps)
	}
	// 改 receiver 方向的两个实测结论见上方注释（正文 §方法集合 动手）。
}

// TestEmbedNotInherit：嵌入的精确方法集合规则。
// 预测：S 拿 T1 的 M1、*T2 的 M3/M4；*S 额外拿 *T1 的 M2。同名遮蔽不虚调（p2 已讲，不重复）。
// Rust 对照: newtype struct S(T1) 不带方法，需手动 impl 或 Deref。
func TestEmbedNotInherit(t *testing.T) {
	if got := dumpMethodSet(S{}); strings.Join(got, ",") != "M1,M3,M4" {
		t.Fatalf("S 方法集合 = %v, 要 [M1 M3 M4]", got)
	}
	if got := dumpMethodSet(&S{}); strings.Join(got, ",") != "M1,M2,M3,M4" {
		t.Fatalf("*S 方法集合 = %v, 要 [M1 M2 M3 M4]", got)
	}
}

// TestTypeAliasMethodSet：别名 vs defined 新类型。
// 预测：Alias 与 T 方法集合相同；Defined 方法集合为空，不能赋给任何要方法的接口。
func TestTypeAliasMethodSet(t *testing.T) {
	var a Alias
	a.Set(7)
	if a.Get() != 7 {
		t.Fatal("别名共享方法集合失败")
	}
	var d Defined
	if got := dumpMethodSet(d); len(got) != 0 {
		t.Fatalf("Defined 方法集合 = %v, 要空", got)
	}
	// 编译期对照（注释矩阵，与正文 §4 呼应）：
	// var _ interface{ Get() int } = Alias{}  // OK
	// var _ interface{ Get() int } = Defined{} // 编译错：Defined does not implement
}

// TestNilErrorTrap：装箱后的 nil 指针不等于 nil 接口。
// 预测：failTrap() 返回的 err != nil（tab 非空、data 为 nil）。
// Rust 对照: Option<Box<dyn Error>> 把"没有"写进类型，无此陷阱。
func TestNilErrorTrap(t *testing.T) {
	err := failTrap()
	if err == nil {
		t.Fatal("预期 err != nil：装箱后接口字非零")
	}
	// 装箱类型断言：err 的动态类型是 *MyErr（data 为 nil 但类型字非零）。
	var target *MyErr
	if !errors.As(err, &target) {
		t.Fatalf("装箱的应是 *MyErr 类型, got %T", err)
	}
	if target != nil {
		t.Fatalf("装箱的是 nil 指针, got %#v", target)
	}
	// 真 nil 对照：
	var e2 error
	if e2 != nil {
		t.Fatal("(nil, nil) 接口应等于 nil")
	}
}

// TestBoxingCost：装箱 bench（正文 §6 实拍数据的来源）。
// 观察：go test -bench BenchmarkBoxing -benchmem 对比 iface/direct 的 ns/op 与 allocs/op。
// 关键：装箱值必须轮换且超出 staticuint64s（0..255 静态表），否则编译器/静态表把分配吃掉；
// 常量 42 装箱实测 0 allocs（正文只讲量级，不引具体 ns）。
// Rust 对照: Box<dyn T> 由借用检查管生命周期，Go 装箱拷贝值。
var (
	sinkAny any
	sinkInt int
)

//go:noinline
func boxSink(n int) { sinkAny = n }

func BenchmarkBoxing(b *testing.B) {
	b.Run("iface", func(b *testing.B) {
		for i := range b.N {
			boxSink(i % 1000) // 0..255 走 staticuint64s 免分配，其余 convT2E 真装箱（正文 §6 实拍）
		}
	})

	b.Run("direct", func(b *testing.B) {
		for i := range b.N {
			sinkInt = i % 1000 // 无装箱
		}
	})
}

// BenchmarkBoxAll：正文 §6 的对照实验，装箱值全在 255 以内 vs 全超出。
// 观察：in255 查 staticuint64s 表免分配（约 1.8ns、0B），above255 每轮真装箱（约 6.8ns、8B、1 allocs/op）。
func BenchmarkBoxAll(b *testing.B) {
	b.Run("above255", func(b *testing.B) {
		for i := range b.N {
			boxSink(256 + i%100)
		}
	})
	b.Run("in255", func(b *testing.B) {
		for i := range b.N {
			boxSink(i % 256)
		}
	})
}

// TestCapReaderChain：包装器链（正文 §8 实拍 HELL 的落点）。
// 改造：在链尾加一个 TrimSpace Reader，输入 " hello " 断言仍是 HELL。
func TestCapReaderChain(t *testing.T) {
	src := strings.NewReader("hello")
	var r io.Reader = NewCapReader(io.LimitReader(src, 4)) // CapReader(LimitReader("hello",4)) → HELL
	c := &CountReader{r: r}
	buf, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != "HELL" {
		t.Fatalf("链输出 = %q, 要 HELL", buf)
	}
	if c.Count == 0 {
		t.Fatal("外层计数 Reader 没被调用")
	}
}

// TestMiddlewareCompose：中间件 = 包装器 + 适配器函数类型。
// 预测：logHandler(authHandler(h)) 外层先跑，未认证请求仍被日志层记录（X-Saw-Log=1）。
// 改造：调换顺序后未认证请求 401 且 X-Saw-Log 缺失，用表驱动断言两种顺序。
func TestMiddlewareCompose(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrap := logHandler(authHandler(h))
	cases := []struct {
		name   string
		auth   string
		sawLog bool
		status int
	}{
		{"no-auth", "", true, http.StatusUnauthorized},
		{"ok-auth", "123456", true, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/greet", nil)
			req.Header.Set("auth", c.auth)
			rec := httptest.NewRecorder()
			wrap.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, 要 %d", rec.Code, c.status)
			}
			if got := rec.Header().Get("X-Saw-Log"); (got == "1") != c.sawLog {
				t.Fatalf("X-Saw-Log = %q, sawLog=%v", got, c.sawLog)
			}
		})
	}
}

// TestSaveBuffer：Save 的落点替换（正文 §Save 实测行）。
// 预测：Save 写 bytes.Buffer 成功且内容一致；无磁盘 IO，跑得比落盘版快得多。
func TestSaveBuffer(t *testing.T) {
	var buf bytes.Buffer
	if err := Save(&buf, []byte("hello")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if buf.String() != "hello" {
		t.Fatalf("buffer = %q, 要 hello", buf.String())
	}
}

// TestHandwrittenJSON：两字段小用例（正文 §9 降级后的练习触点）。
// 预测：Marshal 产出 {"Name":"a","Age":3}；Unmarshal 传非指针报错（typ.Kind()!=Ptr）。
// 完整手写库见 MATERIALS P30 段（reflection/json.go），这里不复制。
func TestHandwrittenJSON(t *testing.T) {
	type Book struct {
		Name string
		Age  int
	}
	// Marshal：走 Type/Field(i) 拿导出字段名，Value/Field(i).Interface() 拿值。
	var b Book
	bv := reflect.ValueOf(&b).Elem()
	bv.Field(0).SetString("a")
	bv.Field(1).SetInt(3)
	var sb strings.Builder
	sb.WriteByte('{')
	for i := range bv.NumField() {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%q:", bv.Type().Field(i).Name)
		fv := bv.Field(i)
		switch fv.Kind() {
		case reflect.String:
			fmt.Fprintf(&sb, "%q", fv.String())
		case reflect.Int:
			fmt.Fprintf(&sb, "%d", fv.Int())
		}
	}
	sb.WriteByte('}')
	if got, want := sb.String(), `{"Name":"a","Age":3}`; got != want {
		t.Fatalf("Marshal = %s, 要 %s", got, want)
	}
	// Unmarshal 入口校验：必须传指针（正文降级为一行的那个要点）。
	_, err := unmarshalNotPtr(b)
	if err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Fatalf("非指针反序列化要报 pointer 错误, got %v", err)
	}
}

// unmarshalNotPtr 复现 reflection/json.go Unmarshal 的入口校验：非指针直接报错。
func unmarshalNotPtr(v any) (any, error) {
	typ := reflect.TypeOf(v)
	if typ.Kind() != reflect.Ptr {
		return nil, errors.New("unmarshal target must be a pointer, got " + typ.Kind().String())
	}
	return reflect.New(typ.Elem()), nil
}

// TestTraceInjection：手动 defer 注入的输出契约（AST 注入版本的预期结果）。
// 预测：enter outer → enter inner → exit inner → exit outer，LIFO 顺序。
// Rust 对照: 无 defer 等价物，用 drop guard（Drop trait 的 RAII）。
func TestTraceInjection(t *testing.T) {
	var buf bytes.Buffer
	old := out
	out = &buf
	defer func() { out = old }()
	traceOuter()
	got := buf.String()
	want := "enter: outer\nenter: inner\ninner-exit: inner\nouter-exit: outer\n"
	if got != want {
		t.Fatalf("trace 输出:\n%q\n要:\n%q", got, want)
	}
}

// TestReflectAPI：Type/Value 主线 API（正文 §9 实测的落点）。
// 预测：指针 Type 必须 Elem() 才有 Name；ValueOf(&i) 不可寻址，Elem() 后可 Set；
// 未导出字段 CanSet=false。
func TestReflectAPI(t *testing.T) {
	pt := reflect.TypeOf(&User{})
	if pt.Name() != "" {
		t.Fatalf("指针 Type 的 Name 应为空, got %q", pt.Name())
	}
	et := pt.Elem()
	if et.Name() != "User" || et.Kind() != reflect.Struct {
		t.Fatalf("Elem 后要 User/struct, got %s/%s", et.Name(), et.Kind())
	}
	// CanAddr 五连测（正文摘录 reflection.go:217-273 的对应断言）。
	i := 10
	v := reflect.ValueOf(&i)
	if v.CanAddr() {
		t.Fatal("ValueOf(&i) 的指针 Value 不可寻址")
	}
	if !v.Elem().CanAddr() || !v.Elem().CanSet() {
		t.Fatal("Elem() 后可寻址可 Set")
	}
	v.Elem().SetInt(8)
	if i != 8 {
		t.Fatalf("Set 没写回原变量: i=%d", i)
	}
	u := User{Name: "a"}
	uv := reflect.ValueOf(&u).Elem()
	if uv.FieldByName("Name").CanSet() != true {
		t.Fatal("导出字段 Name 应可 Set")
	}
	if uv.FieldByName("addr").CanSet() {
		t.Fatal("未导出字段 addr 不可 Set")
	}
	// Call 三步：取 Value、组 []Value、Call。
	out := reflect.ValueOf(Add).Call([]reflect.Value{reflect.ValueOf(3), reflect.ValueOf(4)})
	if out[0].Int() != 7 {
		t.Fatalf("Call 结果 = %d, 要 7", out[0].Int())
	}
}

// go.mod 约束检查：本包挂在根 module golearn（正文 §1 的 1.26 修正实验依托 go1.26.4）。
