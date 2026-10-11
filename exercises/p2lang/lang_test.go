package pkg

import (
	"fmt"
	"strings"
	"testing"
)

// TestInterface 练习隐式实现 + 多态切片。
// 已实现（参考实现见下）：遍历 speakers 拼 Speak()，逗号分隔。
// Rust 对照: fn speak_all(speakers: Vec<Box<dyn Speaker>>) -> String
func speakAll(speakers []Speaker) string {
	// 参考实现：预分配 + 显式循环 + strings.Join。
	// Rust 对照: speakers.iter().map(|s| s.speak()).join(", ")
	// 考点 1：[]Speaker 直接装 Dog/Robot——隐式实现，没有 Box<dyn> 也没有 impl 块。
	// 考点 2：s.Speak() 是动态分派（iface itab 调用），等价 trait object 的 vtable。
	parts := make([]string, 0, len(speakers))
	for _, s := range speakers {
		parts = append(parts, s.Speak())
	}
	return strings.Join(parts, ", ")
}

func TestInterface(t *testing.T) {
	// 注意：Dog 和 Robot 没写任何 "implements" —— 隐式满足。
	got := speakAll([]Speaker{Dog{Name: "rex"}, Robot{ID: 7}})
	want := "rex: woof, robot-7: beep"
	if got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

// TestEmbedding 练习嵌入语义。
// 关键理解：Greeter 拿到的 Hello() 是 Base 上的方法（转发糖），
// 没有多态——给 Base 换个实现不影响 Greeter。
func TestEmbedding(t *testing.T) {
	g := Greeter{Base: Base{Greeting: "hi"}, Count: 2}
	if got := g.Hello(); got != "hi" {
		t.Fatalf("want hi got %q", got)
	}
	// 嵌入 = 字段省略糖：g.Base 和 g 等价访问 Hello。
	if got := g.Base.Hello(); got != "hi" {
		t.Fatalf("explicit path should work too, got %q", got)
	}
}

// TestTypeSwitch 练习 type switch（≈ Rust match 上的 downcast）。
// 已实现（参考实现见下）：string → "str:<len>"，Robot → "robot:<ID>"，其他 → "unknown"。
func describe(v any) string {
	// 参考实现：type switch = Rust match 上的 downcast。
	// 每个分支自动把 v 收窄成对应具体类型（x 已是 string / Robot），
	// 等价 match 的 bound pattern。any = interface{} 的 1.18 别名。
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("str:%d", len(x))
	case Robot:
		return fmt.Sprintf("robot:%d", x.ID)
	default:
		return "unknown"
	}
}

func TestTypeSwitch(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "str:5"},
		{Robot{ID: 3}, "robot:3"},
		{42, "unknown"},
	}
	for _, c := range cases {
		if got := describe(c.in); got != c.want {
			t.Fatalf("describe(%v) = %q want %q", c.in, got, c.want)
		}
	}
}

// TestZeroValue 练习零值可用哲学。
func TestZeroValue(t *testing.T) {
	if got := Join([]string{"a", "b", "c"}, "-"); got != "a-b-c" {
		t.Fatalf("want a-b-c got %q", got)
	}
}

// TestGenerics 练习泛型函数（P1 时代 Go 没有，2022 后才有）。
func TestGenerics(t *testing.T) {
	got := MapAny([]int{1, 2, 3}, func(x int) string { return fmt.Sprint(x * 10) })
	want := []string{"10", "20", "30"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("want %v got %v", want, got)
	}

	evens := Filter([]int{1, 2, 3, 4}, func(x int) bool { return x%2 == 0 })
	if fmt.Sprint(evens) != fmt.Sprint([]int{2, 4}) {
		t.Fatalf("want [2 4] got %v", evens)
	}
}

// TestInterfaceNil 练习 Go 最著名的坑：nil interface ≠ nil 指针。
// Rust 对照：Box<dyn Trait> 是 null 不存在的，Rust 用 Option<Box<dyn T>>。
// Go 的 error 接口常见坑：返回 (T, error) 时 error 里装了个 nil *MyErr
// → err != nil 为 true！
//
// 已实现：maybeFail 修复——nil 时显式 return nil（见下方注释与实现）。
type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func maybeFail(fail bool) error {
	var e *MyErr // nil 指针
	if fail {
		e = &MyErr{msg: "boom"}
	}
	if e == nil {
		return nil // ✅ 修复：显式返回接口零值 (nil type, nil value)
	}
	return e // 具体值非 nil 才装入接口——(type=*MyErr, value≠nil)
}

func TestInterfaceNil(t *testing.T) {
	// 修复后的语义：
	//   interface 底层是 (type, value) 二元组。
	//   坑的根源：return e 把 (type=*MyErr, value=nil) 装进接口——
	//   type 字段非 nil，接口 == nil 判 false，调用方 err != nil 中招。
	//   修法：nil 时显式 return nil，让 type 字段也是 nil。
	// Rust 根本没这个坑：Box<dyn Trait> 不可能是"装着 null 的非空 fat pointer"，
	// Option<Box<dyn T>> 把"没有"表达在类型里。Go 的接口是值语义，坑在语言里。
	if err := maybeFail(false); err != nil {
		t.Fatalf("after fix, want nil, got %v", err)
	}
	// fail 路径仍然工作：
	if err := maybeFail(true); err == nil || err.Error() != "boom" {
		t.Fatalf("fail path broken: %v", err)
	}
}
