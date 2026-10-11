package gcpressure

import (
	"runtime"
	"testing"
)

// 三形态同台：-benchmem 直接读出 allocs/op 与 B/op 差异。
// 协议：min-of-5 交错跑（benchtime=1000000x 钉住次数，防时间片差异）。
func BenchmarkAlloc(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Alloc(1000)
	}
}

func BenchmarkPool(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Pool(1000)
	}
}

func BenchmarkReuse(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Reuse(1000)
	}
}

// GC 账本：强制跑 GC 前后对比 TotalAlloc/HeapAlloc，验证三形态的驻留差异。
// 注意 Alloc 现在把 buf 收进 sinkVar（防折叠），所以 census 里最后要清空它，
// 否则 100k×512B 驻留会污染后续 pool/reuse 的读数——账本实验自己也要记账干净。
func TestGCCensus(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)
	Alloc(100000) // 裸分配：制造 100k 个垃圾对象 + sinkVar 驻留
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("alloc  : GC cycles=%d total=%d MiB heap_live=%d KiB",
		after.NumGC-before.NumGC, (after.TotalAlloc-before.TotalAlloc)/1024/1024, after.HeapAlloc/1024)
	sinkVar = nil // 清驻留

	runtime.ReadMemStats(&before)
	Pool(100000) // pool：稳态零新增，但 any 装箱每次 1 alloc
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("pool   : GC cycles=%d total=%d KiB heap_live=%d KiB",
		after.NumGC-before.NumGC, (after.TotalAlloc-before.TotalAlloc)/1024, after.HeapAlloc/1024)

	runtime.ReadMemStats(&before)
	Reuse(100000) // 复用：全程 1 个对象
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("reuse  : GC cycles=%d total=%d KiB heap_live=%d KiB",
		after.NumGC-before.NumGC, (after.TotalAlloc-before.TotalAlloc)/1024, after.HeapAlloc/1024)
}
