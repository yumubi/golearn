package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"deepsearch/internal/service/search"
)

// bearerRoundTripper adds the service token to every MCP request.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", bearerPrefix+b.token)

	return b.base.RoundTrip(clone)
}

func TestMCPExposesOnlyWebSearch(t *testing.T) {
	want := search.Result{
		Answer:     "the answer",
		Sources:    []search.Source{{URL: "https://example.com", Title: "Example"}},
		Usage:      search.Usage{TotalTokens: 12},
		Model:      "test-model",
		DurationMS: 3,
	}
	httpServer := newTestServer(t, testToken, fakeSearcher{
		search: func(_ context.Context, query string) (search.Result, error) {
			if query != "the question" {
				t.Errorf("query = %q", query)
			}
			return want, nil
		},
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	session, e := client.Connect(context.Background(), &sdkmcp.StreamableClientTransport{
		Endpoint:   httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: testToken, base: http.DefaultTransport}},
	}, nil)
	if e != nil {
		t.Fatalf("connect: %v", e)
	}
	defer func() { _ = session.Close() }()

	tools, e := session.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatalf("list tools: %v", e)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "web_search" {
		t.Fatalf("tools = %+v, want only web_search", tools.Tools)
	}

	result, e := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "the question"},
	})
	if e != nil {
		t.Fatalf("call tool: %v", e)
	}
	if result.IsError {
		t.Fatalf("tool error: %+v", result.Content)
	}

	raw, e := json.Marshal(result.StructuredContent)
	if e != nil {
		t.Fatalf("marshal structured output: %v", e)
	}
	got := search.Result{}
	if e := json.Unmarshal(raw, &got); e != nil {
		t.Fatalf("unmarshal structured output: %v", e)
	}
	if got.Answer != want.Answer || got.Model != want.Model || len(got.Sources) != 1 {
		t.Fatalf("structured output = %+v", got)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %+v", result.Content)
	}
	if text := result.Content[0].(*sdkmcp.TextContent).Text; text != want.Answer {
		t.Fatalf("content text = %q, want %q", text, want.Answer)
	}
}

func TestMCPRequiresToken(t *testing.T) {
	httpServer := newTestServer(t, testToken, fakeSearcher{})

	response := post(t, httpServer, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if body := decodeBody[errorBodyWire](t, response); body.Error.Code != "unauthorized" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}
