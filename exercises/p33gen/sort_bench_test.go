package p33gen

import (
	"slices"
	"sort"
	"testing"
)

// 排序三选手（正文 §2，TBX05#K12；1.18beta2 泛型版快近一倍零分配，
// 1.26 实测三者已几乎无差，见 bench 输出）。
// 数字随机器波动，看量级：本机 M4 两轮 4.46/4.40/4.29 与 3.91/3.94/3.95 ms。

func makeSrc() []int {
	src := make([]int, 100000)
	for i := range src {
		src[i] = (i * 7919) % 1000000
	}
	return src
}

func BenchmarkSortInts(b *testing.B) {
	src := makeSrc()
	for b.Loop() {
		xs := slices.Clone(src)
		sort.Ints(xs)
	}
}

func BenchmarkSlicesSort(b *testing.B) {
	src := makeSrc()
	for b.Loop() {
		xs := slices.Clone(src)
		slices.Sort(xs)
	}
}

func specializeInts(xs []int) { sort.Ints(xs) }

func BenchmarkSpecialized(b *testing.B) {
	src := makeSrc()
	for b.Loop() {
		xs := slices.Clone(src)
		specializeInts(xs)
	}
}
