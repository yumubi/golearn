// retryrun：失败自动重试地执行命令。
// 用法：retryrun [-n 3] [-base 100ms] [-cap 1s] [--] cmd [args...]
// 退出码：0 成功 / 1 执行了但失败 / 2 用法错误（沿用 P20 的分类）。
package main

import (
	"context"
	"errors"
	"fmt"
	"golearn/exercises/p21test"
	"os"
	"os/exec"
	"os/signal"
	"time"
)

// usageErr 用法错误的 sentinel（retryrun.ErrUsage 类比 p20cli.ErrUsage）。
var usageErr = errors.New("usage: retryrun [-n max] [-base d] [-cap d] [--] cmd [args...]")

// parseFlags 手写解析（flag 包遇到 cmd 里的 flag 会打架，工具自身要透传）。
func parseArgs(args []string) (max int, base, capD time.Duration, cmd []string, err error) {
	max = 3
	base = 100 * time.Millisecond
	capD = 2 * time.Second
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i++
			return max, base, capD, args[i:], nil
		case a == "-n":
			i++
			if i >= len(args) {
				return 0, 0, 0, nil, fmt.Errorf("%w: -n needs a value", usageErr)
			}
			if _, err := fmt.Sscanf(args[i], "%d", &max); err != nil {
				return 0, 0, 0, nil, fmt.Errorf("%w: -n %q is not a number", usageErr, args[i])
			}
			if max < 1 {
				return 0, 0, 0, nil, fmt.Errorf("%w: -n must be >= 1", usageErr)
			}
		case a == "-base" || a == "-cap":
			i++
			if i >= len(args) {
				return 0, 0, 0, nil, fmt.Errorf("%w: %s needs a value", usageErr, a)
			}
			d, e := time.ParseDuration(args[i])
			if e != nil {
				return 0, 0, 0, nil, fmt.Errorf("%w: %s %q: %v", usageErr, a, args[i], e)
			}
			if p21test.ClampWait(d) != d || d < 0 {
				return 0, 0, 0, nil, fmt.Errorf("%w: %s must be >= 0", usageErr, a)
			}
			if a == "-base" {
				base = d
			} else {
				capD = d
			}
		case len(a) > 0 && a[0] == '-':
			return 0, 0, 0, nil, fmt.Errorf("%w: unknown flag %q", usageErr, a)
		default:
			return max, base, capD, args[i:], nil
		}
	}
	return 0, 0, 0, nil, usageErr
}

func main() {
	max, base, capD, cmdArgs, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Ctrl-C → ctx 取消 → Retry 立刻停（不打完退避）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := p21test.RetryRunPlan(max, base, capD)
	opts.OnRetry = func(attempt int, wait time.Duration, e error) {
		fmt.Fprintf(os.Stderr, "retryrun: %s\n", p21test.FormatAttempts(attempt, wait, e))
	}

	var runErr error
	attempts := 0
	err = p21test.Retry(ctx, opts, func() error {
		attempts++
		c := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		runErr = c.Run()
		return runErr
	})

	if err != nil {
		// ctx 取消：退出码 130（shell 惯例 SIGINT）。用法错误不会到这里。
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		if ctx.Err() != nil {
			os.Exit(130)
		}
		// 执行了但失败：透传子命令退出码（仅当它是 ExitError 且最后 attempts 次都失败）
		var ee *exec.ExitError
		if attempts > 0 && errors.As(runErr, &ee) {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
}
