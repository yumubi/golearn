package counterbattle

import (
	"fmt"
	"testing"
	"time"
)

var wantTotal = uint64(goroutines * perG)

func TestCorrectness(t *testing.T) {
	m := &MutexCounter{}
	if got := m.Run(true); got != wantTotal {
		t.Fatalf("mutex hot: %d != %d", got, wantTotal)
	}
	a := &AtomicCounter{}
	if got := a.Run(true); got != wantTotal {
		t.Fatalf("atomic hot: %d != %d", got, wantTotal)
	}
	if got := ChannelCounter(true); got != wantTotal {
		t.Fatalf("channel hot: %d != %d", got, wantTotal)
	}
	if got := LocalJoin(true); got != wantTotal {
		t.Fatalf("local hot: %d != %d", got, wantTotal)
	}
}

// run15 单方案 hot 模式跑 15 轮，输出 min/median/max（ms）。
// 15 轮是跟 rust-for-frontend 补测协议对齐的：4 轮会把"离散度不对称"读成"一样快"。
func run15(name string, run func(hot bool) uint64) string {
	var ms []float64
	for i := 0; i < 15; i++ {
		start := time.Now()
		if got := run(true); got != wantTotal {
			return fmt.Sprintf("%s CORRUPT: %d", name, got)
		}
		ms = append(ms, float64(time.Since(start).Microseconds())/1000)
	}
	// 简单排序取 min/median/max
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j] < ms[j-1]; j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
	median := ms[len(ms)/2]
	return fmt.Sprintf("%-8s n=15 min=%6.1f 中位=%6.1f max=%6.1f 极差=%.2fx",
		name, ms[0], median, ms[len(ms)-1], ms[len(ms)-1]/ms[0])
}

// TestBattle 是性能测量, 不是正确性检查: -race 下 channel 减速 10-20×,
// 800 万条 hot 消息必超时(实测 3 次)。压测永远不开 race——见 README 复现命令。
// race 包内检测: 开 race 时自动 skip, `go test -race ./...` 全仓回归不再被拖死。
func TestBattle(t *testing.T) {
	if raceEnabled {
		t.Skip("battle is a benchmark; race detector slowdown invalidates timing (see README)")
	}
	m := &MutexCounter{}
	a := &AtomicCounter{}
	lines := []string{
		run15("mutex", m.Run),
		run15("atomic", a.Run),
		run15("channel", ChannelCounter),
		run15("local", LocalJoin),
	}
	for _, l := range lines {
		t.Log(l)
	}
}
