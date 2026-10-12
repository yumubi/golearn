// Package p20cli：P20 CLI 工具章的练习锚点。
//
// wcgo：微型管道字数统计工具（wc 的教学版）。设计红线来自正文五节：
//   - 库层不摸 os.Stdin/os.Stdout，全走 io.Reader/io.Writer 参数
//   - flag 解析成 config，非法参数返回 error（退出码决定权在 main）
//   - WalkDir 收集错误继续遍历，fs.SkipDir 剪枝
//   - Scanner 必查 Err()，长行要调 Buffer
package p20cli

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ===== 问题 2：flag → config struct（对照 Rust Args::parse）=====

// Config 是 wcgo 的完整配置面。
type Config struct {
	Files   []string // 位置参数；空 + stdin 非 tty = 读管道
	Verbose bool     // -v
	Quiet   bool     // -q
}

// ParseArgs 把参数切成 config。错误一律返回，不 exit。
// （真实 CLI 用 flag 包；这里手写以保留错误处理的可测性，见测试。）
func ParseArgs(args []string) (*Config, error) {
	cfg := &Config{}
	for i := range args {
		switch a := args[i]; {
		case a == "-v":
			cfg.Verbose = true
		case a == "-q":
			cfg.Quiet = true
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("unknown flag %q: %w", a, ErrUsage)
		default:
			cfg.Files = append(cfg.Files, a)
		}
	}
	return cfg, nil
}

// ErrUsage 用法错误 sentinel（退出码 2 的来源）。
var ErrUsage = fmt.Errorf("usage: wcgo [-v] [-q] [files...]")

// ===== 问题 4：退出码分类 =====

// ExitCode 把错误映射到 shell 惯例：nil=0 / 用法=2 / 其他=1。
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case isUsage(err):
		return 2
	default:
		return 1
	}
}

func isUsage(err error) bool {
	for err != nil {
		if err == ErrUsage {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ===== 问题 1 + 3：核心统计（io.Reader 注入，不摸全局）=====

// Stats 是一次统计结果。
type Stats struct {
	Lines, Words, Bytes int64
}

// Count 从 r 统计。长行安全：Scanner 缓冲调到 1MB 并必查 Err()。
func Count(r io.Reader) (Stats, error) {
	var s Stats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 长行上限 1MB
	for sc.Scan() {
		line := sc.Text()
		s.Lines++
		s.Words += int64(len(strings.Fields(line)))
		s.Bytes += int64(len(line)) + 1 // +换行（最后一行无换行时近似，教学包接受）
	}
	if err := sc.Err(); err != nil { // 必查：截断/读错静默不报
		return s, fmt.Errorf("scan: %w", err)
	}
	return s, nil
}

// Run 组装：文件列表或 stdin → 结果写到 w（stdout 由调用方给）。
func Run(cfg *Config, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(cfg.Files) == 0 {
		s, err := Count(stdin)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%d %d %d\n", s.Lines, s.Words, s.Bytes)
		return nil
	}
	var errs []error
	for _, path := range cfg.Files {
		f, err := os.Open(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue // 收集错误继续：一个文件坏不杀死一批
		}
		s, err := Count(f)
		f.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if !cfg.Quiet {
			fmt.Fprintf(stdout, "%d %d %d %s\n", s.Lines, s.Words, s.Bytes, path)
		}
	}
	return errsCombine(errs)
}

// ===== 问题 3：WalkDir 遍历（收集 + 剪枝）=====

// FindGoFiles 遍历 root 返回 .go 文件；错误收集不中止；
// .git 目录整棵剪掉。
func FindGoFiles(root string) (paths []string, errs []error) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			return nil // 记录但继续（return err 会中止全树）
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir // 剪枝：跳过整个子树
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err) // root 本身打不开
	}
	return paths, errs
}

func errsCombine(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%d failures: %w", len(errs), errs[0]) // 简化：计数+首个
}
