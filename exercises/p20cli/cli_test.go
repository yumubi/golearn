package p20cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCountReader 问题 1 改造题答案：核心逻辑收 io.Reader，
// 测试注入 bytes.Buffer/strings.Reader，不碰 os.Stdin。
func TestCountReader(t *testing.T) {
	s, err := Count(strings.NewReader("hello world\nsecond line\n"))
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if s.Lines != 2 || s.Words != 4 {
		t.Fatalf("want lines=2 words=4, got %+v", s)
	}
}

// TestParseFlags 问题 2：非法 flag 返回 ErrUsage（不 exit，
// 退出码决定权在 main 的 ExitCode）。
func TestParseFlags(t *testing.T) {
	cfg, err := ParseArgs([]string{"-v", "a.txt", "b.txt"})
	if err != nil {
		t.Fatalf("-v + files: %v", err)
	}
	if !cfg.Verbose || len(cfg.Files) != 2 {
		t.Fatalf("config 错误: %+v", cfg)
	}
	if _, err := ParseArgs([]string{"--bogus"}); !errors.Is(err, ErrUsage) {
		t.Fatalf("未知 flag 应返回 ErrUsage, got %v", err)
	}
}

// TestExitCode 问题 4：0/1/2 惯例，errors.Is 穿透 %w 识别用法错。
func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{ErrUsage, 2},
		{fmt.Errorf("parse args: %w", ErrUsage), 2}, // 包装链穿透
		{os.ErrNotExist, 1},                         // 文件不存在 = 运行错
		{errors.New("permission denied"), 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Fatalf("ExitCode(%v)=%d, want %d", c.err, got, c.want)
		}
	}
}

// TestWalkCollect 问题 3：部分失败收集继续 + .git 剪枝。
func TestWalkCollect(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	write("sub/a.go", "package sub")
	write(".git/config", "junk")
	write("vendor/x/x.go", "package x") // 不剪 vendor：验证只有 .git 被剪

	paths, errs := FindGoFiles(root)
	if len(errs) != 0 {
		t.Fatalf("健康树不应有错误: %v", errs)
	}
	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "sub/a.go") {
		t.Fatalf("应找到 go 文件: %v", paths)
	}
	if strings.Contains(joined, ".git/") {
		t.Fatalf(".git 应被剪枝: %v", paths)
	}
	if !strings.Contains(joined, "vendor/x/x.go") {
		t.Fatalf("vendor 不剪，应被找到: %v", paths)
	}
}

// TestScanLongLine 问题 3 观察题的代码化：1MB 长行在默认 64KB
// 上限下被截断（Scan 返回 false），调 Buffer 后完整。
func TestScanLongLine(t *testing.T) {
	long := strings.Repeat("x", 512*1024) + "TAIL"
	r := strings.NewReader(long + "\n")
	s, err := Count(r)
	if err != nil {
		t.Fatalf("调过 Buffer 后 512KB 行应完整: %v", err)
	}
	// Scanner 不计换行：Bytes 只在本包 Count 里累加（含换行的字节由
	// bufio 底层读入；这里断言行内容完整而不是字节精确值）。
	if s.Words != 1 {
		t.Fatalf("长行应完整（words=1），got %+v", s)
	}
}

// TestRunPipeline 问题 1 验收：输出进注入的 stdout，无日志混入。
func TestRunPipeline(t *testing.T) {
	cfg, _ := ParseArgs([]string{})
	stdin := strings.NewReader("one two\nthree\n")
	var out, errBuf bytes.Buffer
	if err := Run(cfg, stdin, &out, &errBuf); err != nil {
		t.Fatalf("Run stdin: %v", err)
	}
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), "2 3") {
		t.Fatalf("stdout 应只有统计行, got %q", out.String())
	}
	// 文件模式 + -q 静默
	f := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(f, []byte("x y z\n"), 0o644)
	cfgQ, _ := ParseArgs([]string{"-q", f})
	out.Reset()
	if err := Run(cfgQ, nil, &out, &errBuf); err != nil {
		t.Fatalf("Run -q: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("-q 应零输出, got %q", out.String())
	}
	// 不存在的文件：错误收集，继续处理后续文件
	var out2 bytes.Buffer
	cfg2, _ := ParseArgs([]string{"/nonexistent/file", f})
	err := Run(cfg2, nil, &out2, &errBuf)
	if err == nil {
		t.Fatal("应返回收集到的错误")
	}
	if !strings.Contains(out2.String(), f) {
		t.Fatalf("好文件的结果仍应输出, got %q", out2.String())
	}
	if !strings.Contains(err.Error(), "/nonexistent/file") {
		t.Fatalf("错误应含路径上下文, got %v", err)
	}
}

// TestErrsCombineExtra 保证 errsCombine 的简化行为可见（教学包不做
// errors.Join 全量合并，正文问题 4 讲了完整版）。
func TestErrsCombineExtra(t *testing.T) {
	if errsCombine(nil) != nil {
		t.Fatal("空错误列表应返回 nil")
	}
	err := errsCombine([]error{fmt.Errorf("first"), fmt.Errorf("second")})
	if err == nil || !strings.Contains(err.Error(), "2 failures") || !strings.Contains(err.Error(), "first") {
		t.Fatalf("应计数并带首个错误, got %v", err)
	}
}
