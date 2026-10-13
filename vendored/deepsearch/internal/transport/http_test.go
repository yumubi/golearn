package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"deepsearch/internal/service/search"
	"deepsearch/pkg/httperr"
)

const testToken = "s3cret"

type errorBodyWire struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type fakeSearcher struct {
	search func(ctx context.Context, query string) (search.Result, error)
	fetch  func(ctx context.Context, url string) (search.Page, error)
}

func (f fakeSearcher) Search(ctx context.Context, query string) (search.Result, error) {
	return f.search(ctx, query)
}

func (f fakeSearcher) Fetch(ctx context.Context, url string) (search.Page, error) {
	return f.fetch(ctx, url)
}

// newTestServer mounts the production middleware chain and routes on a fresh
// echo server, mirroring what provider.Http + transport.SetupHttp wire up.
func newTestServer(t *testing.T, authToken string, service Searcher) *httptest.Server {
	t.Helper()

	eh := echo.New()
	eh.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	eh.HTTPErrorHandler = httperr.Handler(false)

	useMiddleware(eh)
	register(eh, service, authToken)

	httpServer := httptest.NewServer(eh)
	t.Cleanup(httpServer.Close)

	return httpServer
}

func post(t *testing.T, httpServer *httptest.Server, path, body, token string) *http.Response {
	t.Helper()

	request, e := http.NewRequest(http.MethodPost, httpServer.URL+path, strings.NewReader(body))
	if e != nil {
		t.Fatalf("new request: %v", e)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, e := httpServer.Client().Do(request)
	if e != nil {
		t.Fatalf("do request: %v", e)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	return response
}

func decodeBody[T any](t *testing.T, response *http.Response) T {
	t.Helper()

	value := new(T)
	if e := json.NewDecoder(response.Body).Decode(value); e != nil {
		t.Fatalf("decode response: %v", e)
	}

	return *value
}

func TestHealthNeedsNoToken(t *testing.T) {
	httpServer := newTestServer(t, testToken, fakeSearcher{})

	response, e := httpServer.Client().Get(httpServer.URL + "/healthz")
	if e != nil {
		t.Fatalf("get health: %v", e)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body := decodeBody[map[string]string](t, response); body["status"] != "ok" {
		t.Fatalf("health body = %v", body)
	}
}

func TestBearerGuard(t *testing.T) {
	service := fakeSearcher{
		search: func(context.Context, string) (search.Result, error) {
			return search.Result{Answer: "42"}, nil
		},
	}

	cases := []struct {
		name    string
		token   string
		request string
		status  int
		code    string
	}{
		{"open when unconfigured", "", "", http.StatusOK, ""},
		{"missing token", testToken, "", http.StatusUnauthorized, "unauthorized"},
		{"wrong token", testToken, "nope", http.StatusUnauthorized, "unauthorized"},
		{"valid token", testToken, testToken, http.StatusOK, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := newTestServer(t, tc.token, service)
			response := post(t, httpServer, "/v1/search", `{"query":"life"}`, tc.request)

			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
			if tc.code == "" {
				return
			}

			body := decodeBody[errorBodyWire](t, response)
			if body.Error.Code != tc.code {
				t.Fatalf("error code = %q, want %q", body.Error.Code, tc.code)
			}
		})
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	httpServer := newTestServer(t, "", fakeSearcher{
		search: func(context.Context, string) (search.Result, error) {
			return search.Result{}, search.ErrEmptyQuery
		},
	})

	response := post(t, httpServer, "/v1/search", `{"query":"  "}`, "")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if body := decodeBody[errorBodyWire](t, response); body.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestSearchRejectsMalformedBody(t *testing.T) {
	httpServer := newTestServer(t, "", fakeSearcher{
		search: func(context.Context, string) (search.Result, error) {
			t.Error("search must not run for a malformed request")
			return search.Result{}, nil
		},
	})

	response := post(t, httpServer, "/v1/search", `{"query":`, "")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if body := decodeBody[errorBodyWire](t, response); body.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestSearchReturnsResult(t *testing.T) {
	want := search.Result{
		Answer:     "the answer",
		Sources:    []search.Source{{URL: "https://example.com", Title: "Example"}},
		Usage:      search.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		Model:      "test-model",
		DurationMS: 7,
	}
	httpServer := newTestServer(t, "", fakeSearcher{
		search: func(_ context.Context, query string) (search.Result, error) {
			if query != "the question" {
				t.Errorf("query = %q", query)
			}
			return want, nil
		},
	})

	response := post(t, httpServer, "/v1/search", `{"query":"the question"}`, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := decodeBody[search.Result](t, response); got.Answer != want.Answer || len(got.Sources) != 1 {
		t.Fatalf("result = %+v", got)
	}
}

func TestFetchErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"ok", nil, http.StatusOK, ""},
		{"invalid url", search.ErrInvalidURL, http.StatusBadRequest, "invalid_request"},
		{"disabled", search.ErrFetchDisabled, http.StatusNotFound, "fetch_disabled"},
		{"unreachable", search.ErrFetchFailed, http.StatusBadGateway, "fetch_failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpServer := newTestServer(t, "", fakeSearcher{
				fetch: func(context.Context, string) (search.Page, error) {
					if tc.err == nil {
						return search.Page{URL: "https://example.com", Title: "Example", Links: []search.Link{}}, nil
					}
					return search.Page{}, tc.err
				},
			})

			response := post(t, httpServer, "/v1/fetch", `{"url":"https://example.com"}`, "")
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
			if tc.code == "" {
				return
			}

			if body := decodeBody[errorBodyWire](t, response); body.Error.Code != tc.code {
				t.Fatalf("error code = %q, want %q", body.Error.Code, tc.code)
			}
		})
	}
}

func TestUnknownRouteUsesErrorEnvelope(t *testing.T) {
	httpServer := newTestServer(t, "", fakeSearcher{})

	response, e := httpServer.Client().Get(httpServer.URL + "/nope")
	if e != nil {
		t.Fatalf("get unknown route: %v", e)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
	if body := decodeBody[errorBodyWire](t, response); body.Error.Code != "not_found" {
		t.Fatalf("error code = %q, want not_found", body.Error.Code)
	}
}
