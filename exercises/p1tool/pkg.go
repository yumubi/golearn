// Package pkg 练习：可见性规则。
package pkg

// Counter 是导出类型（大写开头 = pub）。
type Counter struct {
	n int // 私有字段：外部包直接访问 c.n 会编译错
}

// Add 导出方法。
func (c *Counter) Add(delta int) { c.n += delta }

// N 导出只读访问器。
func (c *Counter) N() int { return c.n }

// newCounter 私有构造器：外部包不能 new。
func newCounter() *Counter { return &Counter{} }

// MustNew 导出构造器，内部用私有构造器。
func MustNew() *Counter { return newCounter() }

// helper 私有函数。
func helper(x int) int { return x * 2 }
