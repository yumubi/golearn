package cmd

import (
	"log/slog"
	"os"
	"time"

	"deepsearch/pkg/slogtrap"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var rootCmd = &cobra.Command{
	Use:   "deepsearch",
	Short: "deepsearch service",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		stdoutLogger := tint.NewHandler(colorable.NewColorable(os.Stderr), &tint.Options{
			AddSource:  true,
			Level:      slog.LevelDebug,
			TimeFormat: time.DateTime,
			NoColor:    !isatty.IsTerminal(os.Stderr.Fd()),
		})

		slog.SetDefault(slog.New(slogtrap.NewFinalHandler(stdoutLogger)))
	},
}

func Execute() {
	cmd, _, e := rootCmd.Find(os.Args[1:])
	// redirect to the default server command if no command is given
	if e == nil && cmd.Use == rootCmd.Use && cmd.Flags().Parse(os.Args[1:]) != pflag.ErrHelp {
		args := append([]string{"server"}, os.Args[1:]...)
		rootCmd.SetArgs(args)
	}

	if e := rootCmd.Execute(); e != nil {
		slog.Error("execute root command failed",
			slog.String("error", e.Error()), slogtrap.ExitCode(1))
	}
}
