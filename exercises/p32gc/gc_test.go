package p32gc

import (
	"runtime"
	"testing"
)

// TestEscapeMatrix：逃逸分析四例（正文 §1，`go build -gcflags="-m"` 输出为准）。
// PREDICT：onStack 不逃逸；onHeap 返回指针必逃逸；big 10240 元素超栈限逃逸；boxed 装箱接口逃逸。
func TestEscapeMatrix(t *testing.T) {
	if onStack() != 3 {
		t.Fatal("onStack")
	}
	if p := onHeap(); p.x != 1 || p.y != 2 {
		t.Fatal("onHeap")
	}
	_ = make([]int, 10240) // escapes to heap（正文 §1 实拍行）
	var boxed any = point{3, 4}
	if bp, ok := boxed.(point); !ok || bp.x != 3 {
		t.Fatal("boxed")
	}
}

type point struct{ x, y int }

func onStack() int {
	p := point{1, 2}
	return p.x + p.y
}

func onHeap() *point {
	p := point{1, 2}
	return &p
}

// TestSemanticGarbage：指针 slice 缩容置 nil 前后的可达性（正文 §4）。
// WATCH：置 nil 前尾部 4KB 仍可达（GC 无能为力），置 nil 后才可回收。
func TestSemanticGarbage(t *testing.T) {
	s := make([]*big, 2)
	s[0] = &big{}
	s[1] = &big{}
	s = s[:1] // 错误写法：底层数组仍引用 s[1]
	if len(s) != 1 {
		t.Fatal("缩容失败")
	}
	t2 := make([]*big, 2)
	t2[0] = &big{}
	t2[1] = &big{}
	t2[1] = nil // 正解：先断引用再缩容
	t2 = t2[:1]
	if len(t2) != 1 {
		t.Fatal("缩容失败")
	}
}

type big struct{ buf [1024]byte }

// TestGCTriggerGoal：GOGC=100 时 goal 约为存活堆的 2 倍（正文 §3 gctrace 行）。
// WATCH：NumGC>0 且最后一条 gctrace 语义由 GODEBUG 打印验证，这里断言 MemStats 一致性。
func TestGCTriggerGoal(t *testing.T) {
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc == 0 || ms.NumGC == 0 {
		t.Fatalf("HeapAlloc=%d NumGC=%d", ms.HeapAlloc, ms.NumGC)
	}
}
