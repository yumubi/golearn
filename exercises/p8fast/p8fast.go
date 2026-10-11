// Package p8fast 练习：P8 快速扩展——每道题对照 notes/p8-fast-track.md 的节号。
//
// ⚠️ 并发契约（counterbattle 实测教训）：练习共享计数前先想清楚要不要重置；
// 热路径用泛型时记住 GC shape（1.2 差异二）。
package p8fast

import (
	"cmp"
	"slices"
	"sync"
)

// ============ 1a. 约束与运算符（notes 1.1）============

// MaxK 泛型 Max——cmp.Ordered 允许比较运算符。
// Rust 对照: fn max_k<T: PartialOrd>(xs: &[T]) -> Option<&T>
// 练习点：空切片返回零值+false（Go 没有 Option，惯例是 (T, ok bool)）。
func MaxK[T cmp.Ordered](xs []T) (T, bool) {
	if len(xs) == 0 {
		var zero T
		return zero, false
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m, true
}

// ============ 1b. underlying type 约束（notes 1.1 的 ~）============

// Total 聚合任意"底层是 []int"的切片（含具名类型 MyInts）。
// Rust 对照: fn total(xs: &[i64]) -> i64 —— Rust 不需要 ~，
// 因为 &MyInts 自动 deref；Go 要显式写底层类型约束。
type MyInts []int

func Total[S ~[]int](s S) int {
	t := 0
	for _, v := range s {
		t += v
	}
	return t
}

// ============ 2a. slices/maps 武器库（notes 2.1）============

// TopN 返回最大的 n 个（保持降序）。用 slices.SortFunc + cmp.Compare。
// Rust 对照: xs.sort_by(cmp); xs.into_iter().take(n)
func TopN[T cmp.Ordered](xs []T, n int) []T {
	cp := slices.Clone(xs)
	slices.SortFunc(cp, func(a, b T) int { return cmp.Compare(b, a) }) // 降序
	if n > len(cp) {
		n = len(cp)
	}
	return cp[:n]
}

// Uniq 去重并排序。用 maps 或 slices——练习点：map 当 set 用再转回切片。
// Rust 对照: xs.into_iter().collect::<HashSet<_>>().into_iter().sorted().collect()
func Uniq[T cmp.Ordered](xs []T) []T {
	seen := make(map[T]struct{}, len(xs))
	out := make([]T, 0, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; !ok {
			seen[x] = struct{}{}
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// ============ 2b. sync.OnceValue（notes 2.3，P5 手写 DI 的官方解）============

// NewCounter 返回懒初始化计数器——第一次调用时构造 100 元素表，之后共享。
// Rust 对照: static COUNTER: OnceLock<Vec<i32>> = OnceLock::new()
// 练习点：体会官方封装如何消灭 P5 的两个翻车（Once 重入/闭包可见性）。
func NewCounter() func() []int {
	return sync.OnceValue(func() []int {
		out := make([]int, 100)
		for i := range out {
			out[i] = i * i
		}
		return out
	})
}

// ============ 3a. table-driven 数据（notes 3.1）============

// FizzBuzz 一行版本——table-driven 测试的目标函数。
func FizzBuzz(n int) string {
	switch {
	case n%15 == 0:
		return "FizzBuzz"
	case n%3 == 0:
		return "Fizz"
	case n%5 == 0:
		return "Buzz"
	default:
		return itoa(n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
