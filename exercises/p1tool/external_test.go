package pkg_test

import (
	"testing"

	p1tool "golearn/exercises/p1tool"
)

// TestVisibility 练习 Go 可见性模型。
// 已实现：通过导出访问器 N() 读取私有字段（编译器拦 c.n，不是 lint）。
//
// 现状：c.n 是私有字段，直接访问会编译错
// （unexported field c.n，编译器拦下，不是 lint）。
//
// Rust 对照：
//
//	struct Counter { n: i32 }        // n 默认私有
//	impl Counter { pub fn n(&self) } // 访问器
//
// Go 没有 pub(crate)/pub(super) 中间档：大写=全导出，小写=包内私有。
func TestVisibility(t *testing.T) {
	c := p1tool.MustNew()
	c.Add(3)
	c.Add(4)

	// 已实现：c.n 编译不过（unexported field），导出访问器 N() 返回 7。
	if got := c.N(); got != 7 {
		t.Fatalf("want 7, got %d", got)
	}
}

// TestPrivateCtor 练习私有构造器。
// 已实现：newCounter 私有，外部包只能走导出 API（MustNew + Add）。
func TestPrivateCtor(t *testing.T) {
	c := p1tool.MustNew()
	c.Add(10)
	if c.N() != 10 {
		t.Fatalf("want 10, got %d", c.N())
	}
	// 已验证：p1tool.newCounter() 在外部包编译不过（实测报文见下），
	// 报错 name newCounter not exported by package pkg，
	// 小写标识符跨包不可见。这是包边界，不是命名约定。
}
