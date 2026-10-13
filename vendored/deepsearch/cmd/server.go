package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"deepsearch/internal/provider"
	"deepsearch/internal/transport"

	"deepsearch/pkg/di"
	"deepsearch/pkg/slogtrap"
	"deepsearch/pkg/util"

	"github.com/spf13/cobra"
)

var serverCmdConfig = struct {
	DotenvPath string
}{}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.PersistentFlags().StringVarP(&serverCmdConfig.DotenvPath, "dotenv", "d", filepath.Join(".", ".env"), "path to .env file")
}

type serverRunner struct {
	Name string
	Exec func(context.Context) error
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the REST API and the MCP endpoint",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())

		ctx = di.New(ctx)
		ctx = context.WithValue(
			ctx,
			provider.DotenvPathCtx{},
			serverCmdConfig.DotenvPath,
		)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

		go func() {
			sig := <-sigCh
			slog.Info("shutdown signal received, stopping services...", slog.String("signal", sig.String()))
			cancel()

			<-sigCh
			slog.Warn(
				"forced shutdown signal received, exiting immediately",
				slog.String("signal", sig.String()),
			)
			os.Exit(1)
		}()

		defer func() {
			slog.Info("shutting down provider...")

			shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer shutdownCancel()

			if e := di.Shutdown(shutdownCtx); e != nil && !errors.Is(e, di.ErrContainerMissing) {
				slog.Error("failed to shutdown provider", slog.String("error", e.Error()))
			}

			slog.Info("shutdown complete, see you next time!")
		}()

		slog.Info("setup provider...")
		if e := provider.Setup(ctx); e != nil {
			slog.Error("setup provider failed", slog.String("error", e.Error()), slogtrap.ExitCode(1))
			return
		}
		slog.Info("provider setup complete")

		runners := []serverRunner{
			{"http", httpRunner},
		}

		var wg sync.WaitGroup
		errCh := make(chan error, len(runners))

		for i, r := range runners {
			wg.Add(1)
			go func(idx int, run serverRunner) {
				defer wg.Done()

				defer func() {
					if r := recover(); r != nil {
						stacks := util.Stacks(1000, 3)
						slog.Error(
							"server runner panic recovered",
							slog.String("name", run.Name),
							slog.String("stack", strings.Join(stacks, ",")),
							slog.Any("panic", r),
						)
						errCh <- fmt.Errorf("runner %s panic: %v", run.Name, r)
						cancel()
					}

					slog.Info("server runner stopped", slog.String("name", run.Name))
				}()

				slog.Info("starting server runner", slog.String("name", run.Name))

				if e := run.Exec(ctx); e != nil {
					if !errors.Is(e, context.Canceled) {
						slog.Error("server runner exited with error", slog.String("name", run.Name), slog.String("error", e.Error()))
						errCh <- e
						cancel()
					}
				}
			}(i, r)
		}

		slog.Info("all server runners started, waiting for signal...")
		wg.Wait()
		close(errCh)

		hasError := false
		for e := range errCh {
			if e != nil {
				hasError = true
				slog.Error("server runner error detected", slog.String("error", e.Error()))
			}
		}

		if hasError {
			os.Exit(1)
		}
	},
}

func httpRunner(ctx context.Context) error {
	httpServer := di.MustInvoke[provider.Http](ctx)

	if e := transport.SetupHttp(ctx); e != nil {
		return fmt.Errorf("setup http server: %w", e)
	}

	slog.Info("http server starting", slog.String("addr", httpServer.StartConfig.Address))

	if e := httpServer.StartConfig.Start(ctx, httpServer); e != nil && !errors.Is(e, http.ErrServerClosed) {
		return fmt.Errorf("start http server: %w", e)
	}

	slog.Info("http server stopped")
	return nil
}
