package pkg

import (
	"fmt"
	"strings"
)

// ============ 练习 1：小接口 + 使用方定义 ============
// Go 惯例：接口定义在使用方（consumer），方法越少越好。
// Rust 对照：trait Speaker { fn speak(&self) -> String; }

type Speaker interface {
	Speak() string
}

// Dog / Robot 都不知道 Speaker 存在——隐式实现。
type Dog struct{ Name string }

func (d Dog) Speak() string { return d.Name + ": woof" }

type Robot struct{ ID int }

func (r Robot) Speak() string { return fmt.Sprintf("robot-%d: beep", r.ID) }

// ============ 练习 2：嵌入（组合，不是继承）============

type Base struct{ Greeting string }

func (b Base) Hello() string { return b.Greeting }

type Greeter struct {
	Base  // 嵌入：方法提升
	Count int
}

// ============ 练习 3：零值可用 ============
// strings.Builder 零值直接能用（≈ Rust 的 String::new() 但无需 mut 绑定规则）

// Join 用零值 Builder 拼接。
func Join(parts []string, sep string) string {
	var b strings.Builder // 零值可用
	for i, p := range parts {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(p)
	}
	return b.String()
}

// ============ 练习 4：泛型 ============
// Rust: fn tally<T: Clone + Ord>(items: Vec<T>) -> BTreeMap<T, usize>

// MapAny 已实现（参考实现）：make 预分配 + 显式循环。
// Rust 对照: fn map_any<T, R>(xs: []T, f: func(T) R) -> Vec<R>，即 xs.iter().map(f).collect()
// 考点：Go 没有 .iter().map() 链——显式循环是主流；泛型语法 [T any, R any] 在函数名后。
// 体会：make([]R, 0, len(xs)) 预分配，append 填充——Go 的 map 不是 iterator 适配器，是 for。
func MapAny[T any, R any](xs []T, f func(T) R) []R {
	out := make([]R, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// Filter[T any](xs []T, keep func(T) bool) []T
func Filter[T any](xs []T, keep func(T) bool) []T {
	out := make([]T, 0)
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
