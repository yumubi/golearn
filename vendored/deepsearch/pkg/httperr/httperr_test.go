package httperr

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

type errorBodyWire struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newTestServer(t *testing.T, exposeError bool, handler echo.HandlerFunc) *httptest.Server {
	t.Helper()

	eh := echo.New()
	eh.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	eh.HTTPErrorHandler = Handler(exposeError)
	eh.GET("/boom", handler)

	httpServer := httptest.NewServer(eh)
	t.Cleanup(httpServer.Close)

	return httpServer
}

func get(t *testing.T, httpServer *httptest.Server, path string) (*http.Response, errorBodyWire) {
	t.Helper()

	response, e := httpServer.Client().Get(httpServer.URL + path)
	if e != nil {
		t.Fatalf("get %s: %v", path, e)
	}
	defer response.Body.Close()

	body := errorBodyWire{}
	if e := json.NewDecoder(response.Body).Decode(&body); e != nil {
		t.Fatalf("decode %s: %v", path, e)
	}

	return response, body
}

func TestHandlerRendersTypedErrors(t *testing.T) {
	httpServer := newTestServer(t, false, func(c *echo.Context) error {
		return BadRequest("query is required")
	})

	response, body := get(t, httpServer, "/boom")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if body.Error.Code != "invalid_request" || body.Error.Message != "query is required" {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestHandlerHidesInternalErrors(t *testing.T) {
	httpServer := newTestServer(t, false, func(*echo.Context) error {
		return errors.New("connection string leaked")
	})

	response, body := get(t, httpServer, "/boom")
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if body.Error.Code != "internal" || body.Error.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestHandlerExposesInternalErrorsInDebug(t *testing.T) {
	httpServer := newTestServer(t, true, func(*echo.Context) error {
		return errors.New("connection string leaked")
	})

	_, body := get(t, httpServer, "/boom")
	if body.Error.Message != "connection string leaked" {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestHandlerMapsUnknownRoute(t *testing.T) {
	httpServer := newTestServer(t, false, func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	response, body := get(t, httpServer, "/missing")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("error = %+v", body.Error)
	}
}
