package p1_5basics

import (
	"slices"
	"testing"
)

// ===== Go 1.21 内建 min/max/clear（T3 特性审计例子） =====

// MinMaxInts 返回切片的最小/最大值（空切片 panic 由调用方保证非空）。
// 用内建 min/max（1.21+，泛型，cmp.Ordered），不用手写 if 比较链。
func MinMaxInts(xs []int) (lo, hi int) {
	lo, hi = xs[0], xs[0]
	for _, x := range xs[1:] {
		lo = min(lo, x)
		hi = max(hi, x)
	}
	return lo, hi
}

// ResetStats 演示 clear（1.21+）：map 清空（len 0 但仍是同一个 map，
// 引用它的地方看得到变化）、slice 元素归零（len 不变）。
func ResetStats(m map[string]int, xs []int) {
	clear(m)
	clear(xs)
}

// MinOf 最小值的多参形态：min 逐对折叠（内建 min 不收 ...xs）。
func MinOf(xs ...int) int {
	lo := xs[0]
	for _, x := range xs[1:] {
		lo = min(lo, x)
	}
	return lo
}

// TestMinMaxBuiltins 内建 min/max：多参、跨类型顺序。
func TestMinMaxBuiltins(t *testing.T) {
	if got := min(3, 1, 2); got != 1 {
		t.Fatalf("min(3,1,2) = %d, want 1", got)
	}
	if got := max(3, 1, 2); got != 3 {
		t.Fatalf("max(3,1,2) = %d, want 3", got)
	}
	// 泛型：float64/string 都行
	if got := min(2.5, 1.5); got != 1.5 {
		t.Fatalf("min(2.5,1.5) = %v, want 1.5", got)
	}
	if got := min("pear", "apple"); got != "apple" {
		t.Fatalf("min strings = %q, want apple", got)
	}
}

// TestMinMaxInts 组合进真实逻辑：切片最值。
func TestMinMaxInts(t *testing.T) {
	lo, hi := MinMaxInts([]int{7, 2, 9, 4})
	if lo != 2 || hi != 9 {
		t.Fatalf("got (%d,%d), want (2,9)", lo, hi)
	}
	if got := MinOf(5, 3, 8, 1); got != 1 {
		t.Fatalf("MinOf = %d, want 1", got)
	}
}

// TestClearBuiltins clear 语义：map 清空（同一引用）、slice 归零
// （len 不变）。
func TestClearBuiltins(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2}
	xs := []int{1, 2, 3}
	ResetStats(m, xs)
	if len(m) != 0 {
		t.Fatalf("map len = %d, want 0", len(m))
	}
	if len(xs) != 3 {
		t.Fatalf("slice len = %d, want 3 (clear 归零不缩容)", len(xs))
	}
	if !slices.Equal(xs, []int{0, 0, 0}) {
		t.Fatalf("slice = %v, want [0 0 0]", xs)
	}
}
