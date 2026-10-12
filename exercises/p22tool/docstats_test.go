package docstats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDirRealPackage 对真实包统计：本包自己就是被测对象（文档覆盖
// 100% 的包解析出 100%）。
func TestDirRealPackage(t *testing.T) {
	r, err := Dir(".")
	if err != nil {
		t.Fatal(err)
	}
	if r.Package != "docstats" {
		t.Fatalf("package = %q, want docstats", r.Package)
	}
	if r.Files < 1 {
		t.Fatalf("files = %d, want >= 1", r.Files)
	}
	if len(r.Undocumented) != 0 {
		t.Fatalf("self should be fully documented, missing: %v", r.Undocumented)
	}
	if r.Ratio() != 1 {
		t.Fatalf("ratio = %.2f, want 1.00", r.Ratio())
	}
	if _, ok := r.Named["Dir"]; !ok {
		t.Fatal("exported func Dir should have doc")
	}
	if _, ok := r.Named["Result.Exporteds"]; !ok {
		t.Fatal("method should be keyed Type.Method")
	}
}

// TestDirUndocumented 构造一个半覆盖的临时包：3 导出、1 有注释。
func TestDirUndocumented(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": "package fake\n\n// Doc'd is documented.\ntype Docd struct{}\n\nfunc Hidden() {}\n\nvar Visible = 1\n",
		"b.go": "package fake\n\nconst ConstNoDoc = 2\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Exporteds() != 4 {
		t.Fatalf("exporteds = %d (%v), want 4", r.Exporteds(), append(r.Undocumented, keys(r.Named)...))
	}
	if r.Documented() != 1 {
		t.Fatalf("documented = %d, want 1", r.Documented())
	}
	got := strings.Join(r.Undocumented, ",")
	want := "ConstNoDoc,Hidden,Visible"
	if got != want {
		t.Fatalf("undocumented = %q, want %q", got, want)
	}
	wantSum := "fake: 1/4 documented (25%), missing: ConstNoDoc,Hidden,Visible"
	if r.Summary() != wantSum {
		t.Fatalf("summary = %q, want %q", r.Summary(), wantSum)
	}
}

// TestDirSyntaxError 坏文件必须报错，不静默跳过。
func TestDirSyntaxError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte("package x\nfunc ("), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Dir(dir); err == nil {
		t.Fatal("syntax error must surface")
	}
}

// TestEmptyPackage 无导出符号的包覆盖率按 1 算（没有可缺的东西）。
func TestEmptyPackage(t *testing.T) {
	dir := t.TempDir()
	src := "package lonely\n\nvar hidden = 1\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ratio() != 1 {
		t.Fatalf("empty package ratio = %.2f, want 1", r.Ratio())
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
