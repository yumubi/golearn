package transport

import (
	"crypto/subtle"
	"log/slog"
	"strings"

	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"

	"deepsearch/pkg/httperr"
)

const bearerPrefix = "Bearer "

// useMiddleware installs the shared middleware chain: panic recovery, request
// id, then the access log.
func useMiddleware(eh *echo.Echo) {
	eh.Use(
		echomiddleware.Recover(),
		echomiddleware.RequestID(),
		accessLogger(),
	)
}

// accessLogger records every request at info level, including the error text of
// failed handlers, so client errors do not pollute the error stream.
func accessLogger() echo.MiddlewareFunc {
	return echomiddleware.RequestLoggerWithConfig(echomiddleware.RequestLoggerConfig{
		LogLatency:       true,
		LogRemoteIP:      true,
		LogHost:          true,
		LogMethod:        true,
		LogURI:           true,
		LogRequestID:     true,
		LogUserAgent:     true,
		LogStatus:        true,
		LogContentLength: true,
		LogResponseSize:  true,
		LogValuesFunc: func(c *echo.Context, v echomiddleware.RequestLoggerValues) error {
			args := []any{
				slog.String("id", v.RequestID),
				slog.String("method", v.Method),
				slog.Int("status", v.Status),
				slog.String("remote_ip", v.RemoteIP),
				slog.String("host", v.Host),
				slog.String("uri", v.URI),
				slog.String("latency_human", v.Latency.String()),
				slog.String("user_agent", v.UserAgent),
				slog.String("bytes_in", v.ContentLength),
				slog.Int64("bytes_out", v.ResponseSize),
			}
			if v.Error != nil {
				args = append(args, slog.String("error", v.Error.Error()))
			}

			c.Logger().Info(v.Method+" "+v.URI, args...)
			return nil
		},
	})
}

// guards returns the route middleware every endpoint but /healthz shares. No
// token configured means every caller is allowed.
func guards(authToken string) []echo.MiddlewareFunc {
	if authToken == "" {
		return nil
	}

	return []echo.MiddlewareFunc{bearerAuth(authToken)}
}

func bearerAuth(token string) echo.MiddlewareFunc {
	expected := []byte(token)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			provided := c.Request().Header.Get(echo.HeaderAuthorization)
			value, ok := strings.CutPrefix(provided, bearerPrefix)
			if !ok || subtle.ConstantTimeCompare([]byte(value), expected) != 1 {
				return httperr.Unauthorized("a valid bearer token is required")
			}

			return next(c)
		}
	}
}
