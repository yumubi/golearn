//go:build race

package p31sched

// race 检测器开着时为真（go test -race 会编入本文件）。
// TestLostUpdateDistribution 故意制造数据竞争复现脏写，-race 下整包
// `go test -race ./...` 必 FAIL，正文给的分跑命令（不带 -race）才有效。
const raceEnabled = true
