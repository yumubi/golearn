package pkg

import (
	"errors"
	"fmt"
	"testing"
)

// ===== errors.AsType（Go 1.26，T3 特性审计例子） =====

// NotFound 是带上下文的域错误（Unwrap 链上的具体类型）。
type NotFound struct {
	What string
}

func (e *NotFound) Error() string { return fmt.Sprintf("%s not found", e.What) }

// LookupErr 构造一个 wrap 链：fmt.Errorf %w 包 *NotFound。
func LookupErr(what string) error {
	return fmt.Errorf("lookup %q: %w", what, &NotFound{What: what})
}

// AsTypeDemo 用 errors.AsType[E]（1.26+）从错误树里取类型：
// 直接返回 (E, bool)，不用先声明变量再传指针（对比 errors.As）。
// 适用边界：E 必须实现 error（编译期检查）；target 是接口或
// 不实现 error 的类型时退回 errors.As（go doc errors.As 的建议：
// most uses prefer AsType）。
func AsTypeDemo(err error) (string, bool) {
	if nf, ok := errors.AsType[*NotFound](err); ok {
		return nf.What, true
	}
	return "", false
}

// AsDemo 老写法对照：errors.As 要先声明变量。
func AsDemo(err error) (string, bool) {
	var nf *NotFound
	if errors.As(err, &nf) {
		return nf.What, true
	}
	return "", false
}

// TestAsTypeVsAs AsType 与 As 等价穿透 %w 链，取法更直接。
func TestAsTypeVsAs(t *testing.T) {
	err := LookupErr("widget")
	what, ok := AsTypeDemo(err)
	if !ok || what != "widget" {
		t.Fatalf("AsType got (%q,%v), want (widget,true)", what, ok)
	}
	what, ok = AsDemo(err)
	if !ok || what != "widget" {
		t.Fatalf("As got (%q,%v), want (widget,true)", what, ok)
	}
	if _, ok := AsTypeDemo(errors.New("other")); ok {
		t.Fatal("AsType should miss unrelated error")
	}
}
