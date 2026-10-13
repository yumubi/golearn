package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"deepsearch/internal/service/search"
	"deepsearch/pkg/util"
)

// webSearchInput is the argument of the web_search MCP tool.
type webSearchInput struct {
	Query string `json:"query" jsonschema:"question or topic to search the web for"`
}

// mcpHandler serves the streamable HTTP MCP endpoint with the web_search tool.
func mcpHandler(service Searcher) http.Handler {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "deepsearch",
		Version: util.BuildVersion(),
	}, &sdkmcp.ServerOptions{
		Instructions: "Call web_search with the question or topic to research. " +
			"It searches the live web and returns a concise answer together with the source URLs it cites.",
	})

	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "web_search",
		Description: "Search the web and return a concise answer with the source URLs it cites. " +
			"Use it for any question that needs current or external knowledge.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, input webSearchInput) (*sdkmcp.CallToolResult, search.Result, error) {
		result, e := service.Search(ctx, input.Query)
		if e != nil {
			return nil, search.Result{}, fmt.Errorf("search failed: %w", e)
		}

		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: result.Answer}},
		}, result, nil
	})

	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, &sdkmcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: true,
		Logger:                     slog.Default(),
	})
}
