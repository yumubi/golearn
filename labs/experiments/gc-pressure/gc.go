// Package gcpressure 实验 1：GC 压力账。
//
// 问题：Go 没有 Rust 的所有权，"每请求分配"到底交多少税？
// 三个形态做同一件事——把 512 字节的 payload 装进 box 传给消费者：
//
//	alloc     每次新分配（dnsproxi-go handleQuery 的现状：每查询 append 拷贝应答）
//	pool      sync.Pool 复用（标准库做法，≈ Rust 的对象池/Arena）
//	reuse     单 buffer 复用写（零分配，≈ Rust &mut 借用语义，单线程下安全）
//
// 对照 Rust：alloc ≈ 每次 Box::new + move；pool ≈ typed_arena；
// reuse ≈ fn(&mut [u8]) 借用传参——Rust 编译器强制你写对，Go 靠自觉+GC 买单。
package gcpressure

import "sync"

const payloadSize = 512

// sinkVar/keep 强制逃逸与防 DCE：Go 没有 std black_box，
// 把 buf 写进包级变量是唯一硬手段（编译器无法证明不逃逸）。
var (
	sinkVar [][]byte
	keep    byte
)

// Alloc 每次新分配一个 512B 切片填数（模拟 per-request 分配）。
// sinkVar 写入强制堆逃逸——否则 make 被栈上分配，0 allocs/op 假象（实测踩过）。
func Alloc(n int) int {
	sinkVar = sinkVar[:0]
	for range n {
		buf := make([]byte, payloadSize)
		for i := range buf {
			buf[i] = byte(i)
		}
		sinkVar = append(sinkVar, buf)
	}
	keep += sinkVar[0][0]
	return int(keep)
}

// Pool 用 sync.Pool 复用 buffer（Get 后 Put 回去）。
// 注意：Get() 返回 any 装箱本身每次 1 alloc——sync.Pool 不是零分配魔法，
// 泛型 pool（如 buffer 各自类型化）或 atomic.Pointer 缓存才可能到 0。
func Pool(n int) int {
	p := sync.Pool{New: func() any { return make([]byte, payloadSize) }}
	for range n {
		buf := p.Get().([]byte)
		for i := range buf {
			buf[i] = byte(i)
		}
		keep += buf[len(buf)-1]
		p.Put(buf)
	}
	return int(keep)
}

// Reuse 整个循环共用一个 buffer（零分配，调用方语义上"独占"）。
func Reuse(n int) int {
	buf := make([]byte, payloadSize)
	for range n {
		for i := range buf {
			buf[i] = byte(i)
		}
		keep += buf[len(buf)-1]
	}
	return int(keep)
}
