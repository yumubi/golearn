// Package p32gc：pprof 四级优化复刻（C:dachang/micro_service#K11，object_pool.go 同构）。
// 对照数据：作者 72070→25587→12454→9625 ns/op；本机 M4 复测见 allocs_bench_test.go 头注释。
package p32gc

import (
	_ "net/http/pprof" // 在线 pprof：go run 后浏览器开 /debug/pprof（main 见正文 §5 运行命令）
	"sync"
)

var (
	scratch = make([]int, 10000)
	bufPool = sync.Pool{
		New: func() any {
			return make([]int, 10000)
		},
	}
)

// recall1：零容量 append，反复扩容（最慢一档）。
func recall1() []int {
	r := []int{}
	for i := range 10000 {
		r = append(r, i)
	}
	r[len(r)-1] = -1
	return r
}

// recall2：一次定容。
func recall2() []int {
	r := make([]int, 10000)
	for i := range 10000 {
		r[i] = i
	}
	r[len(r)-1] = -1
	return r
}

// recall3：全局切片复用，零分配热路径。
func recall3() []int {
	for i := range 10000 {
		scratch[i] = i
	}
	scratch[len(scratch)-1] = -1
	return scratch
}

// recall4：sync.Pool 多 goroutine 安全复用。
// 注意 Get 返回的切片可能被上次 Put 前 append 过（长度非 10000），
// 这里池里只放定容切片，Get 后如长度不符则重建（生产代码常这么写）。
func recall4() []int {
	r, ok := bufPool.Get().([]int)
	if !ok || len(r) != 10000 {
		r = make([]int, 10000)
	}
	for i := range 10000 {
		r[i] = i
	}
	r[len(r)-1] = -1
	out := r[:10000:10000]
	bufPool.Put(r)
	return out
}

// Search：召回 + 过滤，原版四级优化对应的业务调用（这里用 recall2 演示接法）。
func Search() []int {
	result := []int{}
	recallResult := recall2()
	for _, ele := range recallResult { // 召回
		if ele == -1 {
			break
		}
		if ele%1000 == 0 { // 过滤
			result = append(result, ele)
		}
	}
	return result
}
