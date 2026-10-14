// Package p32gc：P32（内存与 GC）练习包。
// 全部测试/基准对应正文 notes/p32-memory-gc.md 的小节。
package p32gc

import "sync"

// Sink 防止编译器把基准对象整体优化掉。
var Sink [][]int

// AppendNoCap：零容量 append 万次，暴露反复扩容复制（正文 §2/§5）。
func AppendNoCap() []int {
	r := []int{}
	for i := range 10000 {
		r = append(r, i)
	}
	r[len(r)-1] = -1
	return r
}

// MakeCap：一次定容，一次分配（正文 §2/§5 recall2）。
func MakeCap() []int {
	r := make([]int, 10000)
	for i := range 10000 {
		r[i] = i
	}
	r[len(r)-1] = -1
	return r
}

// pool：sync.Pool 复用 1 万元素切片（正文 §5 recall4）。
var pool = sync.Pool{
	New: func() any { return make([]int, 10000) },
}

// PoolGet：从池里取切片填数再归还（正文 §5）。
func PoolGet() []int {
	r := pool.Get().([]int)
	for i := range 10000 {
		r[i] = i
	}
	r[len(r)-1] = -1
	pool.Put(r)
	return nil
}
