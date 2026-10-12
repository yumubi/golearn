// cmd/wcgo：P20 的可跑入口（main 薄壳标准形状）。
//
// go run ./exercises/p20cli/cmd/ README.md
// go run ./exercises/p20cli/cmd/ README.md | wc -l   # stdout 纯数据
// go run ./exercises/p20cli/cmd/ /nonexistent; echo "exit=$?"  # 1
// go run ./exercises/p20cli/cmd/; echo "exit=$?"               # 2（无参数无管道）
package main

import (
	"os"

	"golearn/exercises/p20cli"
)

// version 由 ldflags 注入：-ldflags="-X main.version=1.2.3"。
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		os.Stdout.WriteString("wcgo " + version + "\n")
		return
	}
	cfg, err := p20cli.ParseArgs(os.Args[1:])
	if err != nil {
		os.Stderr.WriteString("wcgo: " + err.Error() + "\n")
		os.Exit(p20cli.ExitCode(err))
	}
	err = p20cli.Run(cfg, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		os.Stderr.WriteString("wcgo: " + err.Error() + "\n")
	}
	os.Exit(p20cli.ExitCode(err))
}
