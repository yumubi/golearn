package p20cli

import (
	"os"
	"path/filepath"
	"testing"
)

// ===== os.Root（Go 1.24，T3 特性审计例子） =====

// StatInRoot 限定在 root 目录树内取文件大小：
// os.Root（1.24+）的所有方法只接受"根内相对路径"，.. 逃逸、绝对
// 路径、符号链接出根全部报错。给"扫描用户指定目录"的工具加一道
// 路径安全闸（对比直接 os.Open + filepath.Clean 手防穿越的写法）。
func StatInRoot(root, name string) (int64, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	st, err := r.Stat(name)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// TestRootContains 根内文件可读、大小正确。
func TestRootContains(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := StatInRoot(dir, "a.txt")
	if err != nil || n != 5 {
		t.Fatalf("StatInRoot = (%d,%v), want (5,nil)", n, err)
	}
}

// TestRootEscape 根外逃逸必须被拒（..、绝对路径）。
func TestRootEscape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StatInRoot(dir, filepath.Join("..", filepath.Base(outside), "secret.txt")); err == nil {
		t.Fatal(".. escape must be rejected by os.Root")
	}
	if _, err := StatInRoot(dir, outside+"/secret.txt"); err == nil {
		t.Fatal("absolute path must be rejected by os.Root")
	}
}
