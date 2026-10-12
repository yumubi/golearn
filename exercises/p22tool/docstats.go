// Package docstats 解析 Go 包的"文档覆盖率"：
// 导出类型/函数/方法/常量里，带 doc 注释的比例。
//
// 它本身就是文档工程的练习：godoc 站点（pkg.go.dev）上"有没有文档"
// 直接取决于源码里的 doc comment。用 AST 数一遍，比口说有据。
//
// 用法（库层只做解析，入口在 cmd/docstats）：
//
//	r, err := docstats.Dir("./exercises/p1tool")
//	fmt.Println(r.Exporteds(), r.Documented(), r.Ratio())
package docstats

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Result 一个包的统计。字段导出以便调用方二次加工。
type Result struct {
	// Package 是包名（多文件包取第一个声明）。
	Package string
	// Files 是解析的 .go 文件数（不含 _test.go）。
	Files int
	// Named 是各导出符号的 doc 注释（nil = 没写注释）。
	Named map[string]string
	// Undocumented 是没有 doc 注释的导出标识符名列表（排序后）。
	Undocumented []string
}

// Exporteds 返回导出标识符总数。
func (r Result) Exporteds() int { return len(r.Named) + len(r.Undocumented) }

// Documented 返回带 doc 注释的导出标识符数。
func (r Result) Documented() int { return len(r.Named) }

// Ratio 返回文档覆盖率（0 到 1；无导出符号时返回 1）。
func (r Result) Ratio() float64 {
	n := r.Exporteds()
	if n == 0 {
		return 1
	}
	return float64(r.Documented()) / float64(n)
}

// Summary 一行人读的摘要。
func (r Result) Summary() string {
	return fmt.Sprintf("%s: %d/%d documented (%.0f%%), missing: %s",
		r.Package, r.Documented(), r.Exporteds(), r.Ratio()*100,
		strings.Join(r.Undocumented, ","))
}

// Dir 解析目录下全部非测试 Go 文件并统计文档覆盖率。
// 语法错误返回 error（文档统计工具不该静默跳过坏文件）。
func Dir(path string) (Result, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, path, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return Result{}, err
	}
	res := Result{Named: map[string]string{}}
	for name, pkg := range pkgs {
		res.Package = name
		res.Files = len(pkg.Files)
		for _, file := range pkg.Files {
			collect(file, res.Named, &res.Undocumented)
		}
	}
	return res, nil
}

// collect 从一个 AST 文件里挑导出符号。函数/方法取 FuncDecl.Doc；
// const/var/type 块取 spec.Doc，为 nil 时回退 GenDecl.Doc（godoc
// 语义：单 spec 块的注释挂在 decl 上，parser 才这么给）。
func collect(file *ast.File, named map[string]string, missing *[]string) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			// 方法记成 Type.Method，godoc 也这么组织
			key := d.Name.Name
			if d.Recv != nil {
				if recv := recvTypeName(d.Recv); recv != "" {
					key = recv + "." + d.Name.Name
				}
			}
			addDoc(key, d.Doc, named, missing)
		case *ast.GenDecl:
			// const/var/type 块：单 spec 时注释挂 GenDecl.Doc，
			// 多 spec 各自挂 spec.Doc；nil 时回退块注释
			for _, spec := range d.Specs {
				var doc *ast.CommentGroup
				switch s := spec.(type) {
				case *ast.TypeSpec:
					doc = s.Doc
				case *ast.ValueSpec:
					doc = s.Doc
				}
				if doc == nil && len(d.Specs) == 1 {
					doc = d.Doc
				}
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						addDoc(s.Name.Name, doc, named, missing)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							addDoc(n.Name, doc, named, missing)
						}
					}
				}
			}
		}
	}
}

// addDoc 登记一个符号：有 doc 收 Named，没有进 missing。
func addDoc(name string, doc *ast.CommentGroup, named map[string]string, missing *[]string) {
	if doc == nil {
		*missing = append(*missing, name)
		sort.Strings(*missing)
		return
	}
	named[name] = doc.Text()
}

// recvTypeName 拿方法接收者的类型名（跳过 *T 的星号）。
func recvTypeName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// Version 打印进 CLI 的版本号，-ldflags 注入点（P22 正文讲的正是它）。
var Version = "dev"

// TrimmedPath 返回 Version 的展示形式（占位：保持包小）。
func TrimmedPath() string { return filepath.Clean(Version) }
