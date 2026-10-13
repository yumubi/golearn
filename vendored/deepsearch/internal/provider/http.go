package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"deepsearch/pkg/di"
	"deepsearch/pkg/httperr"

	"github.com/labstack/echo/v5"
)

// Http is the shared echo server. The transport layer registers middleware and
// routes on it, and the server runner serves it with StartConfig.
type Http struct {
	*echo.Echo
	StartConfig echo.StartConfig
}

func SetupHttp(ctx context.Context) error {
	if e := di.Provide(ctx,
		func(ctx context.Context) (Http, error) {
			env := di.MustInvoke[Env](ctx)
			logger := di.MustInvoke[Logger](ctx)

			startConfig := echo.StartConfig{
				Address:    env.HttpAddr,
				HideBanner: true,
				HidePort:   true,
				BeforeServeFunc: func(server *http.Server) error {
					server.ReadTimeout = env.ReadTimeout
					server.ReadHeaderTimeout = env.ReadTimeout
					server.WriteTimeout = env.WriteTimeout
					server.IdleTimeout = env.IdleTimeout
					server.MaxHeaderBytes = env.MaxHeaderBytes
					server.BaseContext = func(net.Listener) context.Context {
						return ctx
					}
					return nil
				},
			}

			eh := echo.NewWithConfig(echo.Config{
				Logger:             logger.Logger,
				HTTPErrorHandler:   HTTPErrorHandler(env.StdLogLevel == slog.LevelDebug),
				FormParseMaxMemory: env.MaxMultipartMemory,
			})

			eh.IPExtractor = echo.LegacyIPExtractor()

			return Http{Echo: eh, StartConfig: startConfig}, nil
		},
		di.WithCleanRecursive(false),
	); e != nil {
		return fmt.Errorf("failed to provide http: %w", e)
	}

	return nil
}

// HTTPErrorHandler renders every handler error as {"error":{"code","message"}}.
func HTTPErrorHandler(exposeError bool) echo.HTTPErrorHandler {
	return httperr.Handler(exposeError)
}
