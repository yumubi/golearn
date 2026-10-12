package p1_5basics

import (
	"strings"
	"testing"
)

// 预测题：先在纸上写答案，再跑 go test -v 对答案。
// 每个测试对应正文一节的考点，断言就是"标准答案"。

// TestIota §2：iota 从 0 起每行 +1；KB/MB/GB 是 1<<10/1<<20/1<<30。
func TestIota(t *testing.T) {
	if Sunday != 0 || Wednesday != 3 || Saturday != 6 {
		t.Fatalf("iota 行号错误: Sunday=%d Wednesday=%d Saturday=%d", Sunday, Wednesday, Saturday)
	}
	if KB != 1<<10 || MB != 1<<20 || GB != 1<<30 {
		t.Fatalf("iota 表达式错误: KB=%d MB=%d GB=%d", KB, MB, GB)
	}
	// 枚举无类型安全：Weekday(99) 编译通过——这是和 Rust enum 的本质差异
	var w Weekday = 99
	_ = w
}

// TestSliceShare §4 预测题 1：sub := s[1:3] 改 sub[0]，s[1] 变不变？
// append(sub, 5) 后 s[3] 变不变？
func TestSliceShare(t *testing.T) {
	a := []int{1, 2, 3, 4}
	got := MutateViaSlice(a)
	// 共享底层数组：s[1] 必然变成 99
	if got[1] != 99 {
		t.Fatalf("a[1] 应为 99（子切片共享底层数组），got %d", got[1])
	}
	b := []int{1, 2, 3, 4}
	AppendToSub(b)
	// cap 够：append 写进 b 的底层数组，b[3] 变 5
	if b[3] != 5 {
		t.Fatalf("b[3] 应为 5（append 未换底层数组），got %d", b[3])
	}
	// 隔离版：CloneIsolated 不影响原切片
	c := []int{1, 2, 3, 4}
	_ = CloneIsolated(c)
	if c[2] != 3 || c[3] != 4 {
		t.Fatalf("隔离副本不应影响原切片，c=%v", c)
	}
}

// TestMapZero §5 预测题 2：缺键返回什么？comma-ok 怎么区分？
func TestMapZero(t *testing.T) {
	m := map[string]int{"a": 1}
	// 缺键得零值，不报错
	if v := m["missing"]; v != 0 {
		t.Fatalf("缺键应返回零值 0，got %d", v)
	}
	// comma-ok 是唯一区分手段
	if _, ok := Lookup(m, "a"); !ok {
		t.Fatal("存在的键 ok 应为 true")
	}
	if _, ok := Lookup(m, "missing"); ok {
		t.Fatal("缺键 ok 应为 false")
	}
	// nil map 能读不能写（写入 panic，这里只验证读）
	var nilMap map[string]int
	if v := nilMap["any"]; v != 0 {
		t.Fatalf("nil map 读应得零值，got %d", v)
	}
}

// TestRuneIndex §6 预测题 4：len 按字节、rune 数按字符。
func TestRuneIndex(t *testing.T) {
	s := "Go语言" // Go=2B, 语=3B, 言=3B
	if len(s) != 8 {
		t.Fatalf("len 按字节应为 8，got %d", len(s))
	}
	if RuneCount(s) != 4 {
		t.Fatalf("rune 数应为 4，got %d", RuneCount(s))
	}
	// s[2] 是"语"的第一个字节（232），不是 '语'
	if b := ByteAt(s, 2); b != 232 {
		t.Fatalf("s[2] 应为字节 232，got %d", b)
	}
	// 按 rune 索引才是第 n 个字符
	if r := CharAt(s, 2); r != '语' {
		t.Fatalf("CharAt(2) 应为 '语'，got %q", r)
	}
}

// TestDeferOrder §8 预测题 3：LIFO + 参数立即求值。
func TestDeferOrder(t *testing.T) {
	var out []string
	DeferLog(&out)
	// 后注册的先执行
	want := "body,second-defer,first-defer"
	if got := strings.Join(out, ","); got != want {
		t.Fatalf("LIFO 顺序错误:\nwant %s\ngot  %s", want, got)
	}
	// 参数立即求值：返回值是 after，但 defer 里的参数是 before
	if got := DeferValue(); got != "after" {
		t.Fatalf("函数返回应为新值 after，got %s", got)
	}
}

// TestGrade §7：无表达式 switch。
func TestGrade(t *testing.T) {
	cases := map[int]string{95: "A", 90: "A", 89: "B", 60: "B", 59: "F", 0: "F"}
	for score, want := range cases {
		if got := Grade(score); got != want {
			t.Fatalf("Grade(%d)=%s, want %s", score, got, want)
		}
	}
}

// TestPointerReceiver §10 改造题答案：值接收者不在 T 的方法集里，
// *Counter 才满足 Adder。
func TestPointerReceiver(t *testing.T) {
	var c Counter
	c.Add(3)   // 可寻址值自动取址
	c.Add(4)
	if c.Value() != 7 {
		t.Fatalf("counter 应为 7，got %d", c.Value())
	}
	// 接口满足：*Counter 有 Add 在方法集里，Counter 没有
	type adder interface{ Add(int) }
	var _ adder = &c
	// var _ adder = c   // 编译错：Counter 的方法集没有 Add——预测题答案
	_ = c
}

// TestValueSemantics §12：struct 拷贝传参改不到原值，slice 元素共享。
func TestValueSemantics(t *testing.T) {
	c := Counter{n: 1}
	MutateStruct(c) // 拷贝：原值不动
	if c.Value() != 1 {
		t.Fatalf("struct 按值拷贝，原值应仍为 1，got %d", c.Value())
	}
	xs := []int{1, 2, 3}
	MutateSlice(xs) // 共享底层数组：xs[0] 被改
	if xs[0] != 100 {
		t.Fatalf("slice 元素共享，xs[0] 应为 100，got %d", xs[0])
	}
}
