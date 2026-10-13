package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"

	"deepsearch/internal/provider"
	"deepsearch/internal/service/search"
	"deepsearch/pkg/di"
	"deepsearch/pkg/httperr"
)

// Searcher is the search service the transport serves.
type Searcher interface {
	Search(ctx context.Context, query string) (search.Result, error)
	Fetch(ctx context.Context, url string) (search.Page, error)
}

// SetupHttp registers the middleware chain and the routes on the shared echo
// server of the provider layer.
func SetupHttp(ctx context.Context) error {
	env, e := di.Invoke[provider.Env](ctx)
	if e != nil {
		return fmt.Errorf("invoke env: %w", e)
	}

	service, e := di.Invoke[*search.Service](ctx)
	if e != nil {
		return fmt.Errorf("invoke search service: %w", e)
	}

	httpServer, e := di.Invoke[provider.Http](ctx)
	if e != nil {
		return fmt.Errorf("invoke http server: %w", e)
	}

	useMiddleware(httpServer.Echo)
	register(httpServer.Echo, service, env.AuthToken)

	return nil
}

func register(eh *echo.Echo, service Searcher, authToken string) {
	guards := guards(authToken)

	eh.GET("/healthz", handleHealth)
	eh.POST("/v1/search", handleSearch(service), guards...)
	eh.POST("/v1/fetch", handleFetch(service), guards...)
	eh.Any("/mcp", echo.WrapHandler(mcpHandler(service)), guards...)
}

type searchRequest struct {
	Query string `json:"query"`
}

func handleSearch(service Searcher) echo.HandlerFunc {
	return func(c *echo.Context) error {
		request := searchRequest{}
		if e := c.Bind(&request); e != nil {
			return httperr.BadRequest(`request body must be a JSON object with a string "query"`)
		}

		result, e := service.Search(c.Request().Context(), request.Query)
		if e != nil {
			if errors.Is(e, search.ErrEmptyQuery) {
				return httperr.BadRequest(e.Error())
			}

			return httperr.BadGateway("search_failed", e.Error())
		}

		return c.JSON(http.StatusOK, result)
	}
}

type fetchRequest struct {
	URL string `json:"url"`
}

func handleFetch(service Searcher) echo.HandlerFunc {
	return func(c *echo.Context) error {
		request := fetchRequest{}
		if e := c.Bind(&request); e != nil {
			return httperr.BadRequest(`request body must be a JSON object with a string "url"`)
		}

		page, e := service.Fetch(c.Request().Context(), request.URL)
		switch {
		case errors.Is(e, search.ErrInvalidURL):
			return httperr.BadRequest(e.Error())
		case errors.Is(e, search.ErrFetchDisabled):
			return httperr.NotFound("fetch_disabled", e.Error())
		case errors.Is(e, search.ErrFetchFailed):
			return httperr.BadGateway("fetch_failed", e.Error())
		case e != nil:
			return httperr.Internal()
		}

		return c.JSON(http.StatusOK, page)
	}
}

func handleHealth(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
