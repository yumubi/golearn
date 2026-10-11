package pkg

import (
	"errors"
	"fmt"
	"testing"
)

// Go 错误处理：error 就是个接口。
//
//	type error interface { Error() string }
//
// Rust: Result<T, E> 是类型，? 传播，编译器强制 unwrap 处理。
// Go:   error 是普通返回值，if err != nil 是习惯不是语法。
//
//	—— 没有 ?，没有 try/catch，panic 只用于程序员 bug（≈ Rust panic!，不是 recoverable error）。
//
// sentinal error（≈ Rust 的单元变体枚举）:
var ErrEmpty = errors.New("empty input")

// divide 已实现（参考实现，下方）：b == 0 返回包装 ErrDivZero 的错误，否则 a/b。
// 题目要求：b == 0 → (0, 包装 ErrDivZero 的错误)；否则 (a/b, nil)。
//
// Rust 对照：
//
//	#[derive(thiserror::Error)]
//	enum DivError { Zero }
//	fn divide(a: i64, b: i64) -> Result<i64, DivError>
//
// Go 惯例：
//
//	var ErrDivZero = errors.New("divide by zero")
//	fmt.Errorf("divide %d by %d: %w", a, b, ErrDivZero)  // %w = 包装（≈ err.source()）
//	errors.Is(err, ErrDivZero) 判断（≈ matches!(e, DivError::Zero)）
var (
	ErrDivZero = errors.New("divide by zero")
)

func divide(a, b int64) (int64, error) {
	// 参考实现：%w 包装 sentinel（≈ thiserror #[source]）。
	// errors.Is 沿 Unwrap 链穿透判真；消息带 a/b 上下文，
	// 测试第 53 行拒绝裸 sentinel——上下文是给看日志的人的。
	if b == 0 {
		return 0, fmt.Errorf("divide %d by %d: %w", a, b, ErrDivZero)
	}
	return a / b, nil
}

func TestErrorWrapping(t *testing.T) {
	_, err := divide(1, 0)
	if err == nil {
		t.Fatal("want error for zero divisor")
	}
	if !errors.Is(err, ErrDivZero) {
		t.Fatalf("want ErrDivZero wrapped, got %v", err)
	}
	// 包装链要保留上下文（≈ Rust 的 thiserror context）：
	if got := err.Error(); got == ErrDivZero.Error() {
		t.Fatalf("want wrapped context message, got bare %q", got)
	}

	v, err := divide(9, 3)
	if err != nil || v != 3 {
		t.Fatalf("want 3, got %d err %v", v, err)
	}
}

// lookup 已实现（参考实现，下方）：不存在时返回 *NotFoundError，
// 测试用 errors.As 断言类型并取出 Key（≈ downcast）。
func lookup(m map[string]int, key string) (int, error) {
	// 参考实现：自定义错误类型 + errors.As downcast。
	// ≈ Rust: m.get(key).ok_or(NotFoundError { key })，
	// errors.As 是安全的 downcast_ref（沿 Unwrap 链穿透）。
	// 注意返回 &NotFoundError{}（指针）——Error() 定义在指针接收者上。
	v, ok := m[key]
	if !ok {
		return 0, &NotFoundError{Key: key}
	}
	return v, nil
}

func TestErrorAs(t *testing.T) {
	_, err := lookup(map[string]int{"a": 1}, "b")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want *NotFoundError, got %T %v", err, err)
	}
	if nf.Key != "b" {
		t.Fatalf("want key b, got %s", nf.Key)
	}
}

// 顺手体会 go vet 会抓什么：取消下面这行注释再跑 go vet ./exercises/p1tool，
// 你会看到 "fmt.Sprintf format %d has arg "string" of wrong type string"。
// var _ = fmt.Sprintf("not a bug just a demo %d", "string")
