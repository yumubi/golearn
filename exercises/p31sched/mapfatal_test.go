package p31sched

import "testing"

// TestMapFatal：并发写 map 的 fatal 不可 recover（正文 §8）。
// skip-ok: 真跑会 fatal error: concurrent map writes 崩掉整个测试进程，
// recover 接不住（fatal 不是 panic）；正文 §8 已实拍输出与退出码 1。
// 现场说明：两个 goroutine 各写 1 万个 key 到同一个 map 即触发
// internal/runtime/maps.fatal（panic.go:1181），套 defer recover() 同样 fatal。
// 修复用 sync.Map 或 RWMutex 包 map。
func TestMapFatal(t *testing.T) {
	t.Skip("skip-ok: 正文已实拍，真跑会崩测试进程（fatal 不可 recover）")
}
