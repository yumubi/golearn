// Package p4ejdb 练习：读 ejdb 后亲手推导它的关键机制。
// 载体源码：/Users/akka/oss/github.com/clarkqaq/ejdb（只读，不改动）
package p4ejdb

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// docIDToKey 复刻 ejdb/db.go:279 的键编码（练习目标：不看源码写对它）。
func docIDToKey(id int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(id))
	return buf
}

// TestKeyEncoding 验证：大端序让"整数序 == 字节序"。
func TestKeyEncoding(t *testing.T) {
	// 已推导并填入预期：1 → 00…01，2 → 00…02，10 → 00…0a（int64(1) 大端 = 00 00 00 00 00 00 00 01）。
	cases := []struct {
		id   int64
		want string // hex 期望，练习者填
	}{
		{1, "0000000000000001"},
		{2, "0000000000000002"},
		{10, "000000000000000a"},
	}
	for _, c := range cases {
		got := fmt.Sprintf("%x", docIDToKey(c.id))
		if got != c.want {
			t.Fatalf("docIDToKey(%d) = %s, want %s", c.id, got, c.want)
		}
	}

	// 关键性质：字节比较 == 整数比较（正数域）。
	ids := []int64{1, 2, 10, 100, 1000}
	for i := 1; i < len(ids); i++ {
		a, b := docIDToKey(ids[i-1]), docIDToKey(ids[i])
		if string(a) >= string(b) {
			t.Fatalf("key order broken: %d vs %d", ids[i-1], ids[i])
		}
	}
}

// TestKeyEncodingNegative 复刻 ejdb 没做符号位翻转的坑。
// 已回答（结论见下）。
// 结论：docIDToKey(-1) 的字节比 docIDToKey(1) 【大】。
// 原因：uint64(-1) = MaxUint64 = 0xFFFF...FF（最高位 1），
// 而 1 的编码是 0x00...01（最高位 0）。大端序里最高字节先比，
// 0xFF > 0x00 → -1 的 key 排在 1 后面。字典序 = 无符号整数序，
// 负数全被排到正数"上面"（正确的序被劈成两半）。
// ejdb 没踩坑纯属侥幸：docID 从 1 自增，永远是正数，没有负 key。
// I64 索引就躲不开这个问题——见 i64ToKey 的符号位翻转。
func TestKeyEncodingNegative(t *testing.T) {
	neg := docIDToKey(-1)
	pos := docIDToKey(1)
	// 已填：biggerThanPositive = true（ejdb 里 ID 从 1 自增，所以主键没踩坑）
	biggerThanPositive := string(neg) > string(pos)
	if !biggerThanPositive {
		t.Fatal("expected -1 key to sort AFTER 1 key (no sign-bit flip)")
	}
}

// TestI64IndexOrder 复刻 jsondoc/value.go 的符号位翻转技巧。
// ejdb 的 I64 索引编码：把符号位翻转，使 -1 < 0 < 1 在字节序里成立。
func i64ToKey(v int64) []byte {
	buf := make([]byte, 8)
	// 符号位翻转：最高位 XOR 1（等价写法：uint64(v) ^ 0x8000000000000000）
	binary.BigEndian.PutUint64(buf, uint64(v)^1<<63)
	return buf
}

func TestI64IndexOrder(t *testing.T) {
	vals := []int64{math.MinInt64, -1000, -1, 0, 1, 1000, math.MaxInt64}
	for i := 1; i < len(vals); i++ {
		a, b := i64ToKey(vals[i-1]), i64ToKey(vals[i])
		if string(a) >= string(b) {
			t.Fatalf("signed order broken: %d vs %d", vals[i-1], vals[i])
		}
	}
}

// TestConsumeClosure 理解 docConsumer 闭包模式（query_exec.go:76）。
// 实现 takeFirstN：返回一个 consumer，收到 n 个文档后返回 false 停止扫描。
// Rust 对照：.take(n) 组合子。
func takeFirstN(n int64, sink *[][]byte) func(id int64, doc []byte) bool {
	// 参考实现：闭包变量 counted 是这个"迭代器对象"的状态——
	// Go 用闭包干 Rust struct + impl Iterator 的活（query_exec.go:76 的
	// docConsumer：返回 false = 停止扫描，≈ .take(n) 的短路语义）。
	// sink 走指针：闭包经它写文档，返回值只管"还收不收"。
	counted := int64(0)
	return func(id int64, doc []byte) bool {
		if counted >= n {
			return false
		}
		counted++
		*sink = append(*sink, doc)
		return true
	}
}

func TestConsumeClosure(t *testing.T) {
	var sink [][]byte
	consume := takeFirstN(3, &sink)
	accepted := 0
	for i := range 10 {
		if !consume(int64(i), []byte(fmt.Sprintf("doc-%d", i))) {
			break
		}
		accepted++
	}
	if accepted != 3 || len(sink) != 3 {
		t.Fatalf("want stop after 3, accepted %d sink %d", accepted, len(sink))
	}
}
