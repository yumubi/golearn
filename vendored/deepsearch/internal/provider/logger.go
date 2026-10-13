package provider

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"deepsearch/pkg/di"
	"deepsearch/pkg/slogtrap"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

type Logger struct {
	*slog.Logger
}

func SetupLogger(ctx context.Context) error {
	if e := di.Provide(
		ctx,
		func(ctx context.Context) (Logger, error) {
			env := di.MustInvoke[Env](ctx)

			output := os.Stderr
			handler := tint.NewHandler(colorable.NewColorable(output), &tint.Options{
				AddSource:  true,
				Level:      env.StdLogLevel,
				TimeFormat: time.DateTime,
				NoColor:    !isatty.IsTerminal(output.Fd()),
			})

			logger := slog.New(slogtrap.NewFinalHandler(handler))
			slog.SetDefault(logger)

			return Logger{Logger: logger}, nil
		},
		di.WithCleanRecursive(false),
	); e != nil {
		return fmt.Errorf("failed to provide logger: %w", e)
	}

	return nil
}
