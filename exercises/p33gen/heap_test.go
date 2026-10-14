package p33gen

import (
	"container/heap"
	"testing"
)

// AbstractHeap 泛型抽象（对照 C:dachang/algorithm/common.go 的 value+orderBy 二元组）
type AbstractHeap[K comparable, V any] struct {
	items   []V
	orderBy func(a, b V) bool
}

func (h *AbstractHeap[_, V]) Len() int           { return len(h.items) }
func (h *AbstractHeap[_, V]) Less(i, j int) bool { return h.orderBy(h.items[i], h.items[j]) }
func (h *AbstractHeap[_, V]) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *AbstractHeap[_, V]) Push(x any)         { h.items = append(h.items, x.(V)) }
func (h *AbstractHeap[_, V]) Pop() any {
	n := len(h.items)
	it := h.items[n-1]
	h.items = h.items[:n-1]
	return it
}

func TestAbstractHeap(t *testing.T) {
	h := &AbstractHeap[string, int]{orderBy: func(a, b int) bool { return a < b }}
	heap.Init(h)
	for _, v := range []int{5, 1, 3} {
		heap.Push(h, v)
	}
	if got := heap.Pop(h).(int); got != 1 {
		t.Fatal(got)
	}
	if got := heap.Pop(h).(int); got != 3 {
		t.Fatal(got)
	}
}
