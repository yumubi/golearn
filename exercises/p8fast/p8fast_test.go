package p8fast

import (
	"slices"
	"sync"
	"testing"
)

// ---- 1a. MaxK ----

func TestMaxK(t *testing.T) {
	tests := []struct {
		name string
		xs   []int
		want int
		ok   bool
	}{
		{"empty", nil, 0, false},
		{"single", []int{7}, 7, true},
		{"max first", []int{9, 1, 2}, 9, true},
		{"max last", []int{1, 2, 9}, 9, true},
		{"negatives", []int{-5, -1, -9}, -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MaxK(tt.xs)
			if ok != tt.ok || got != tt.want {
				t.Errorf("MaxK(%v) = (%d,%v), want (%d,%v)", tt.xs, got, ok, tt.want, tt.ok)
			}
		})
	}
	// 泛型对 string 也成立（cmp.Ordered 覆盖）
	if g, _ := MaxK([]string{"go", "rust", "zig"}); g != "zig" {
		t.Errorf("MaxK strings = %q, want zig", g)
	}
}

// ---- 1b. underlying type 约束 ----

func TestTotalUnderlying(t *testing.T) {
	var xs MyInts = []int{1, 2, 3}
	if Total(xs) != 6 { // MyInts 满足 ~[]int
		t.Fatalf("Total(MyInts) = %d, want 6", Total(xs))
	}
	if Total([]int{10, 20}) != 30 { // 普通切片也满足
		t.Fatalf("Total([]int) = %d, want 30", Total([]int{10, 20}))
	}
}

// ---- 2a. TopN / Uniq ----

func TestTopN(t *testing.T) {
	xs := []int{5, 1, 9, 3, 7}
	got := TopN(xs, 3)
	want := []int{9, 7, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("TopN = %v, want %v", got, want)
	}
	// 原切片不被修改（Clone 语义）
	if !slices.Equal(xs, []int{5, 1, 9, 3, 7}) {
		t.Fatalf("TopN mutated input: %v", xs)
	}
	// n 超长时返回全部
	if got := TopN([]int{2, 1}, 10); !slices.Equal(got, []int{2, 1}) {
		t.Fatalf("TopN overflow = %v", got)
	}
}

func TestUniq(t *testing.T) {
	got := Uniq([]int{3, 1, 3, 2, 1, 9})
	want := []int{1, 2, 3, 9}
	if !slices.Equal(got, want) {
		t.Fatalf("Uniq = %v, want %v", got, want)
	}
}

// ---- 2b. OnceValue：懒初始化 + 单例（P5 手写 DI 的官方解对照）----

func TestOnceValue(t *testing.T) {
	calls := 0
	get := sync.OnceValue(func() []int {
		calls++
		return []int{1, 2, 3}
	})
	a := get()
	b := get() // 第二次必须返回缓存
	if calls != 1 {
		t.Fatalf("provider called %d times, want 1（OnceValue 单例语义坏了）", calls)
	}
	if !slices.Equal(a, b) {
		t.Fatal("two different instances")
	}
	// 并发调用也只构造一次（对照 P5 TestProvideInvoke 的并发语义）
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); get() }()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("concurrent: called %d times, want 1", calls)
	}
}

// ---- 3a. FizzBuzz（table-driven 演示给用户看的主体）----

func TestFizzBuzz(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "1"}, {3, "Fizz"}, {5, "Buzz"},
		{15, "FizzBuzz"}, {30, "FizzBuzz"}, {7, "7"}, {99, "Fizz"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FizzBuzz(tt.n); got != tt.want {
				t.Errorf("FizzBuzz(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}
