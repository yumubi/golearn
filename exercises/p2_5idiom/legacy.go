// legacy.go：预置的"外来户代码"样板——能跑，但每一段都踩了 P2.5
// 讲的惯例红线。重构任务：对照 idiom.go 的参考实现逐个修掉。
// 测试（idiom_test.go）验证重构后行为不变——这是重构的第一纪律。
package p2_5idiom

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// RedLine 1：胖接口定义在实现方（应在使用方定义 + 拆单方法）。
type DataStore interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context) ([]string, error)
	Size(ctx context.Context) (int, error)
	Clear(ctx context.Context) error
	Close(ctx context.Context) error
}

// RedLine 4：getter 加了 Get（惯例：c.Count() 不叫 c.GetCount()）。
type StatsLegacy struct{ hits int }

// GetCount 是坏命名。
func (s StatsLegacy) GetCount() int { return s.hits }

// PyramidProcess 是 else 金字塔（惯例：guard clause 提前返回）。
func PyramidProcess(input string) string {
	if input == "" {
		return ""
	} else {
		if strings.Contains(input, "\n") {
			return ""
		} else {
			return strings.ToUpper(input)
		}
	}
}

// LooseError 丢上下文（惯例：%w 包装 + 每层加"这层在干什么"）。
func LooseError(key string) error {
	_ = key
	return errors.New("failed") // 调用方不知道哪层失败的
}

// SentinelCompare 用 == 比较（惯例：errors.Is，包装链会断 ==）。
func SentinelCompare(err error) bool {
	return err == ErrNotFound // errors.New 返回的指针 == 只在未包装时成立
}

// SwallowAndLog 双重处理（惯例：错误要么上抛要么记日志，不同时做）。
func SwallowAndLog(err error, log *[]string) error {
	*log = append(*log, "ERROR: "+err.Error()) // 记了日志
	return fmt.Errorf("wrapped: %w", err)      // 又上抛——上层会再记一次
}
