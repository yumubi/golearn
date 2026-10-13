package httperr

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
)

// Error is an HTTP failure carrying a stable machine-readable code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

func BadRequest(message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: message}
}

func Unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: message}
}

func NotFound(code, message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: code, Message: message}
}

func BadGateway(code, message string) *Error {
	return &Error{Status: http.StatusBadGateway, Code: code, Message: message}
}

func Internal() *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal server error"}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Handler renders every error an echo server produces as
// {"error":{"code","message"}}. Internal error text is exposed only when
// exposeError is set, so production responses never leak implementation
// details.
func Handler(exposeError bool) echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if response, _ := echo.UnwrapResponse(c.Response()); response != nil && response.Committed {
			return
		}

		status := http.StatusInternalServerError
		var statusCoder echo.HTTPStatusCoder
		if errors.As(err, &statusCoder) {
			if code := statusCoder.StatusCode(); code != 0 {
				status = code
			}
		}

		rendered := errorDetail{Code: CodeForStatus(status), Message: http.StatusText(status)}

		var api *Error
		var httpError *echo.HTTPError
		switch {
		case errors.As(err, &api):
			status = api.Status
			rendered = errorDetail{Code: api.Code, Message: api.Message}
		case errors.As(err, &httpError):
			rendered = errorDetail{Code: CodeForStatus(status), Message: httpError.Message}
			if exposeError {
				if wrapped := httpError.Unwrap(); wrapped != nil {
					rendered.Message = wrapped.Error()
				}
			}
		default:
			if exposeError {
				rendered.Message = err.Error()
			}
		}

		if rendered.Message == "" {
			rendered.Message = http.StatusText(status)
		}

		if status >= http.StatusInternalServerError {
			c.Logger().Error("request failed", slog.Int("status", status), slog.String("error", err.Error()))
		}

		var writeErr error
		if c.Request().Method == http.MethodHead {
			writeErr = c.NoContent(status)
		} else {
			writeErr = c.JSON(status, errorBody{Error: rendered})
		}
		if writeErr != nil {
			c.Logger().Error("failed to send error response", slog.String("error", writeErr.Error()))
		}
	}
}

func CodeForStatus(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusUnauthorized:
		return "unauthorized"
	}

	if status >= 400 && status < 500 {
		return "invalid_request"
	}

	return "internal"
}
