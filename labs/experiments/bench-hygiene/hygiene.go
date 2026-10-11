// Package benchhygiene 实验 3：benchmark 卫生——三个现场翻车与修法。
//
// 这个包本身不跑压测，它是 results-2026-10-10.md E3 的"活体教材"：
// 三种 benchmark 假象的演示代码 + 证明它们会发生/被抓住的测试。
// 翻车数据见 results E3 表格，这里保留最小复现。
package benchhygiene

// Foldable 未防折叠的写法：结果无人消费，编译器可能把循环优化掉。
// （Go 编译器目前不删这种循环，但不同版本/架构不保证——
//
//	这是"防折叠是纪律不是信仰"的原因。）
func Foldable(n int) int {
	sum := 0
	for i := range n {
		sum += i
	}
	return sum
}

// Escaped 防折叠写法：结果写进包级变量（Go 无 std black_box）。
var Sink int

func Escaped(n int) int {
	sum := 0
	for i := range n {
		sum += i
	}
	Sink = sum // 写包级 = 消费了 = 编译器不敢删
	return sum
}
