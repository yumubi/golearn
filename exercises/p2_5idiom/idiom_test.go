// Package p2_5idiom：legacy.go 是"外来户代码"样板（能跑但不地道），
// idiom.go 是重构后的参考实现。测试验证两边行为一致——重构不改行为。
package p2_5idiom

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestInterfaceDesign 练习 1：胖接口拆小 + 使用方定义。
// 改造前：legacy.DataStore（7 方法，定义在实现方）。
// 改造后：KeyReader/Writer（单方法，定义在使用方），MemoryStore 隐式满足。
func TestInterfaceDesign(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// 写入方只需要 Writer 能力
	var w Writer = store
	if err := w.Put(ctx, "k", "v"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// 读取方只需要 KeyReader 能力
	var r KeyReader = store
	got, err := r.Get(ctx, "k")
	if err != nil || got != "v" {
		t.Fatalf("Get: %v %q", err, got)
	}
	// 隐式满足：store 从没声明 implements KeyReader
}

// TestErrorChain 练习 2：sentinel + %w 链 + errors.Is。
func TestErrorChain(t *testing.T) {
	ctx := context.Background()
	_, err := LoadKey(ctx, NewMemoryStore(), "missing")
	if err == nil {
		t.Fatal("应返回错误")
	}
	// errors.Is 穿透 %w 链识别 sentinel（== 比较会断链——预测题答案）
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应穿透识别 ErrNotFound，got %v", err)
	}
	// 因果链完整：顶层消息包含每层上下文
	if !strings.Contains(err.Error(), "load key") || !strings.Contains(err.Error(), "get \"missing\"") {
		t.Fatalf("错误消息应含完整因果链，got %q", err.Error())
	}
}

// TestGuardClause 练习 3：提前返回 vs else 金字塔——行为等价。
func TestGuardClause(t *testing.T) {
	if _, err := Process(""); err == nil {
		t.Fatal("空输入应报错")
	}
	if _, err := Process("a\nb"); err == nil {
		t.Fatal("多行应报错")
	}
	if got, err := Process("hello world"); err != nil || got != "HELLO WORLD" {
		t.Fatalf("normal: %v %q", err, got)
	}
	// 对照：legacy.PyramidProcess 输出一致（重构不改行为的证据）
	if got := PyramidProcess("hello world"); got != "HELLO WORLD" {
		t.Fatalf("legacy 版行为应一致，got %q", got)
	}
}

// TestGetter 练习 4：getter 命名 Count()，不是 GetCount()。
func TestGetter(t *testing.T) {
	var s Stats
	s.SetHits(42)
	if s.Count() != 42 {
		t.Fatalf("Count 应为 42，got %d", s.Count())
	}
}

// TestTableDriven 练习 5：table-driven 标准形状。
func TestTableDriven(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already clean", "abc", "abc"},
		{"mixed spaces", "  Hello   World ", "hello world"},
		{"tabs and newlines", "A\tB\nC", "a b c"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Fatalf("Normalize(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestFake 改造题答案：手写 fake（不用 mock 框架）。
// 接口小所以 fake 也小——P2.5 问题 2 的直接推论。
type fakeReader struct{ data map[string]string }

func (f fakeReader) Get(_ context.Context, key string) (string, error) {
	if v, ok := f.data[key]; ok {
		return v, nil
	}
	return "", ErrNotFound
}

func TestFake(t *testing.T) {
	ctx := context.Background()
	fake := fakeReader{data: map[string]string{"k": "v"}}
	got, err := LoadKey(ctx, fake, "k")
	if err != nil || got != "v" {
		t.Fatalf("fake 注入: %v %q", err, got)
	}
	// 传一个没实现完整 store 的最小 fake 也满足 KeyReader——单方法接口的红利
}
