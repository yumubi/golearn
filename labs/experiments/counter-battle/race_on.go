//go:build race

package counterbattle

// raceEnabled: -race 编译时为 true（build tag 是唯一可靠探测方式；
// 环境变量探测不可靠）。TestBattle 用它 skip——压测配 race 是方法论错误，
// 详见 counters_test.go 注释与 README。
var raceEnabled = true
