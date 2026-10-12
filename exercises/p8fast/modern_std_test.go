package p8fast

import (
	"encoding/json"
	"iter"
	"strings"
	"testing"
)

// ===== strings.SplitSeq（Go 1.24，T3 特性审计例子） =====

// FieldLens 返回每段（按逗号分）的长度序列。
// SplitSeq（1.24+）是迭代器版 Split：不分配切片，
// 大输入 + 早停（take 3）时省一半分配。
func FieldLens(s string) iter.Seq[int] {
	return func(yield func(int) bool) {
		for part := range strings.SplitSeq(s, ",") {
			if !yield(len(part)) {
				return
			}
		}
	}
}

// TestSplitSeq 迭代器版 Split：值序同 Split、可早停。
func TestSplitSeq(t *testing.T) {
	var got []int
	for n := range FieldLens("go,rust,ts,python") {
		got = append(got, n)
	}
	want := []int{2, 4, 2, 6}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// 早停：只取前 2 段
	count := 0
	for range FieldLens("a,b,c,d") {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Fatalf("early stop count = %d, want 2", count)
	}
}

// ===== omitzero（Go 1.24 json 标签，T3 特性审计例子） =====

// Event 演示 omitzero：零值字段（含 IsZero() bool 自定义零值）整体
// 省略。对比 omitempty：只对空 slice/map/串/0 起作用，struct 零值
// 照样输出（这是 omitempty 的老坑）。
type Event struct {
	Name string `json:"name"`
	At   int64  `json:"at,omitempty"`
	Meta Meta   `json:"meta,omitzero"` // 1.24 起结构体零值也能省
}

// Count 包着 IsZero：自定义零值判断，omitzero 认这个接口。
type Count struct {
	N int
}

func (c Count) IsZero() bool { return c.N == 0 }

type Meta struct {
	Count Count  `json:"count,omitzero"` // IsZero() true → 省掉
	Host  string `json:"host,omitempty"`
}

// TestOmitZero 零值 Meta 整个省掉；非零时照常输出。
func TestOmitZero(t *testing.T) {
	b, err := json.Marshal(Event{Name: "hit"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "meta") {
		t.Fatalf("zero meta must be omitted, got %s", b)
	}
	b, err = json.Marshal(Event{Name: "hit", Meta: Meta{Host: "a1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"host":"a1"`) {
		t.Fatalf("nonzero meta must stay, got %s", b)
	}
}

// ===== B.Loop（Go 1.24，T3 特性审计例子） =====

// BenchmarkFieldLensLoop b.Loop（1.24+）：替代老 b.N 计数循环，
// 框架自动处理计时起点/收敛，防止编译器把不变量提出循环（P8 第一课
// 的防折叠），代码量减半。
func BenchmarkFieldLensLoop(b *testing.B) {
	for b.Loop() {
		for range FieldLens("a,b,c,d,e,f,g") {
		}
	}
}
