package p21test

import (
	"errors"
	"strings"
	"testing"

	"golearn/exercises/p20cli"
)

// FuzzParseArgs 对 P20 的 wcgo 参数解析器做性质测试（fuzz 目标）。
// 性质（不变量，任何输入都必须满足）：
//  1. 不 panic（解析器是纯函数，坏输入只该产生错误）
//  2. 错误必然包装 p20cli.ErrUsage（解析器只可能报这一类错）
//  3. ExitCode 映射只落 {0, 1, 2}（shell 退出码惯例）
//
// fuzz 参数只支持基本类型，这里用空格分隔串编码 argv，
// strings.Fields 还原（文件名含空格的场景由单元测试覆盖）。
func FuzzParseArgs(f *testing.F) {
	seeds := []string{
		"",                        // 空参数
		"-v",                      // 仅 verbose
		"-q",                      // 仅 quiet
		"-v -q a.txt",             // 混排
		"-x",                      // 未知 flag → ErrUsage
		"file1 file2 -v",          // 文件后置 flag
		"-verbose",                // 长前缀（HasPrefix 命中）
		"-v -v -v f",              // 重复 flag
		"文件-µ-🎉 -v",               // unicode 文件名
		strings.Repeat("-a", 100), // 超长串
		"- -v -q",                 // 单横线文件名
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, cmdline string) {
		args := strings.Fields(cmdline)
		cfg, err := p20cli.ParseArgs(args)

		// 性质 1：cfg 与 err 的组合必须自洽（不 panic 已由不崩保证）
		if err == nil && cfg == nil {
			t.Fatalf("ParseArgs(%q) = nil, nil; want cfg on success", args)
		}

		// 性质 2：任何错误都必须包装 ErrUsage
		if err != nil && !errors.Is(err, p20cli.ErrUsage) {
			t.Fatalf("ParseArgs(%q) error %v does not wrap ErrUsage", args, err)
		}

		// 性质 3：ExitCode 只落 {0, 1, 2}，且与错误自洽
		code := p20cli.ExitCode(err)
		if err == nil && code != 0 {
			t.Fatalf("nil error maps to %d, want 0", code)
		}
		if err != nil && code != 2 {
			t.Fatalf("usage error %v maps to %d, want 2", err, code)
		}
	})
}
