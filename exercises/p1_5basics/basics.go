// Package p1_5basics：P1.5 语法速通的练习锚点。
//
// 每个函数对应正文一节的考点；测试文件里是预测题（先写答案再跑）。
// Rust 对照注释保留，供迁移者锚定。
package p1_5basics

import (
	"strings"
	"unicode/utf8"
)

// ===== §2 常量与 iota =====

// Weekday 演示 iota 枚举（无类型安全：Weekday(99) 编译通过）。
type Weekday int

const (
	Sunday Weekday = iota // 0
	Monday                // 1（iota 隐式重复）
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

// Size 演示 iota 表达式：每行 <<10。
const (
	_  = iota // 跳过 0
	KB = 1 << (10 * iota)
	MB
	GB
)

// ===== §3 for/range 全形态 =====

// Sum 返回切片元素和（三种 for 形态里选最合适的）。
func Sum(xs []int) int {
	total := 0
	for _, v := range xs { // range 二形态：值拷贝
		total += v
	}
	return total
}

// ===== §4 切片底层 =====

// MutateViaSlice 演示子切片共享底层数组：改 sub[0] 就是改 s[1]。
// 返回修改后的 s，调用方（测试）用它验证"共享"这一事实。
func MutateViaSlice(s []int) []int {
	sub := s[1:3]
	sub[0] = 99
	return s
}

// AppendToSub 演示 append 在 cap 够时不换底层数组：
// s[3] 会被 append 写进来。改成三索引 s[1:3:3] 可隔离（改造题）。
func AppendToSub(s []int) []int {
	sub := s[1:3]
	sub = append(sub, 5)
	return s
}

// CloneIsolated 返回 a[1:3] 的隔离副本（防共享的标准动作）。
func CloneIsolated(a []int) []int {
	sub := a[1:3:3]
	out := make([]int, len(sub))
	copy(out, sub)
	return out
}

// ===== §5 map 三坑 =====

// Lookup 演示 comma-ok：区分"键不存在"和"值就是零值"。
func Lookup(m map[string]int, key string) (int, bool) {
	v, ok := m[key]
	return v, ok
}

// ===== §6 字符串与 rune =====

// RuneCount 返回 s 的字符数（不是字节数）。
func RuneCount(s string) int { return utf8.RuneCountInString(s) }

// ByteAt 返回 s 第 i 个字节（可能不是完整字符）。
func ByteAt(s string, i int) byte { return s[i] }

// CharAt 返回 s 第 i 个字符（rune）。索引按 rune 计——必须先转 []rune。
func CharAt(s string, i int) rune {
	rs := []rune(s)
	return rs[i]
}

// ===== §7 switch 变体 =====

// Grade 演示无表达式 switch（if-else 链的整洁版）。
func Grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 60:
		return "B"
	default:
		return "F"
	}
}

// ===== §8 defer 三语义 =====

// DeferLog 收集字符串按 defer 执行顺序追加。
// 闭包捕获引用（对比参数立即求值），调用方验证 LIFO。
func DeferLog(out *[]string) {
	defer func() { *out = append(*out, "first-defer") }()
	defer func() { *out = append(*out, "second-defer") }()
	*out = append(*out, "body")
}

// DeferValue 演示参数立即求值：返回值里 v 是 defer 行那一刻的拷贝。
func DeferValue() string {
	v := "before"
	defer func(msg string) { _ = msg }("captured:" + v) // 参数此刻求值
	v = "after"
	return v // 调用方断言返回 "after"，而 defer 里的 msg 是 "captured:before"
}

// ===== §10 方法接收者 =====

// Counter 值/指针接收者选型的练习对象。
type Counter struct{ n int }

// Add 要改接收者 → 指针接收者。
func (c *Counter) Add(d int) { c.n += d }

// Value 只读 → 值接收者。
func (c Counter) Value() int { return c.n }

// ===== §12 值语义 vs 引用语义 =====

// MutateStruct 传结构体拷贝：改不到调用方原值。
func MutateStruct(c Counter) { c.n = 100 }

// MutateSlice 共享底层数组：改得到元素，append 改不到长度。
func MutateSlice(xs []int) { xs[0] = 100 }

// ===== 练习工具 =====

// Join 用零值 strings.Builder（零值可用的代表，P2 问题 4 预演）。
func Join(parts []string, sep string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(p)
	}
	return b.String()
}
