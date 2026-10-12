// Package p2_5idiom：P2.5 惯用法的练习锚点。
//
// legacy.go（同目录）预置了"能跑但不地道"的代码；本文件是改造后的
// 参考实现接口面。练习任务见测试文件注释与 P2.5 动手节。
package p2_5idiom

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ===== 练习 1：接口定义在使用方 + 拆小 =====

// legacy.go 里有个 7 方法的胖接口 DataStore 定义在实现方。
// 惯用改法：使用方按需定义单方法接口。

// KeyReader 是 fetcher 使用方自定义的接口（单方法）。
type KeyReader interface {
	Get(ctx context.Context, key string) (string, error)
}

// Writer 是写入使用方自定义的接口（单方法）。
type Writer interface {
	Put(ctx context.Context, key, value string) error
}

// MemoryStore 具体类型实现全部方法——接口满足是隐式的，
// 它不知道 KeyReader/Writer 存在。
type MemoryStore struct {
	data map[string]string
}

// NewMemoryStore 零值不够用（map 零值 nil 不可写），所以提供构造器。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]string)}
}

// Get 实现 KeyReader。
func (m *MemoryStore) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("get %q: %w", key, ErrNotFound)
	}
	return v, nil
}

// Put 实现 Writer。
func (m *MemoryStore) Put(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

// Delete 实现 legacy 胖接口的其余方法（保持能力不丢）。
func (m *MemoryStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// ===== 练习 2：错误链（sentinel + %w 包装 + errors.Is）=====

// ErrNotFound sentinel error：命名规范 ErrXxx。
var ErrNotFound = errors.New("not found")

// LoadKey 演示错误链：每层加一句"这层在干什么"，顶层读出完整因果。
func LoadKey(ctx context.Context, r KeyReader, key string) (string, error) {
	v, err := r.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("load key: %w", err)
	}
	return v, nil
}

// ===== 练习 3：guard clause =====

// Process 演示提前返回：错误路径立即 return，主逻辑零缩进。
// legacy.go 的 Pyramid 版本是 else 套 else（对比着看）。
func Process(input string) (string, error) {
	if input == "" {
		return "", errors.New("empty input")
	}
	if strings.Contains(input, "\n") {
		return "", errors.New("multiline not allowed")
	}
	return strings.ToUpper(input), nil // 主逻辑零缩进
}

// ===== 练习 4：getter 不加 Get =====

// Stats getter 命名：Count() 不叫 GetCount()。
type Stats struct{ hits int }

// Count 只读访问器（不加 Get——Go 惯例）。
func (s Stats) Count() int { return s.hits }

// SetHits setter 用 SetXxx。
func (s *Stats) SetHits(n int) { s.hits = n }

// ===== 练习 5：table-driven（形状示范，改造在测试侧）=====

// Normalize 演示表驱动测试的目标函数：去空白 + 小写。
func Normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
