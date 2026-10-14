package p32gc

import "testing"

// 三级分配对照（正文 §2/§5，数字为 M4/go1.26.4 实测）：
//
//	BenchmarkAppendNoCap-10  42202 ns/op  357630 B/op  19 allocs/op
//	BenchmarkMakeCap-10       7024 ns/op   81921 B/op   1 allocs/op
//	BenchmarkPool-10          2748 ns/op      24 B/op   1 allocs/op
//
// micro_service 四级优化原版（作者注释）：72070 → 25587 → 12454 → 9625 ns/op。

func BenchmarkAppendNoCap(b *testing.B) {
	for b.Loop() {
		r := AppendNoCap()
		Sink = append(Sink[:0], r)
	}
}

func BenchmarkMakeCap(b *testing.B) {
	for b.Loop() {
		r := MakeCap()
		Sink = append(Sink[:0], r)
	}
}

func BenchmarkPool(b *testing.B) {
	for b.Loop() {
		PoolGet()
	}
}
