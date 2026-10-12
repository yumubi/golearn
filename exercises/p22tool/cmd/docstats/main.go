// docstats 命令行入口：打印一个或多个包的文档覆盖率。
//
// 用法：
//
//	go run ./exercises/p22tool/cmd/docstats ./exercises/p1tool ./exercises/p22tool
//
// 退出码：0 全部有文档；1 有缺失；2 用法错误（P20 的惯例延续）。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"golearn/exercises/p22tool"
)

func main() {
	showDoc := flag.Bool("d", false, "打印每个已记录符号的 doc 首行")
	showVersion := flag.Bool("version", false, "打印版本号退出")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "docstats [-d] [-version] <dir> [<dir>...]")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if *showVersion {
		fmt.Println(docstats.Version)
		return
	}
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	missingAny := false
	for _, dir := range args {
		r, err := docstats.Dir(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, dir+":", err)
			os.Exit(2)
		}
		fmt.Println(r.Summary())
		if *showDoc {
			for name, text := range r.Named {
				first := text
				if i := indexNewline(text); i >= 0 {
					first = text[:i]
				}
				fmt.Printf("    %s: %s\n", name, first)
			}
		}
		if len(r.Undocumented) > 0 {
			missingAny = true
		}
	}
	if missingAny {
		os.Exit(1)
	}
}

func indexNewline(s string) int {
	return strings.IndexByte(s, '\n')
}
