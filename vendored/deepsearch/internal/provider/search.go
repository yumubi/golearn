package provider

import (
	"context"
	"fmt"

	"deepsearch/internal/service/search"
	"deepsearch/pkg/agentutil/tools"
	"deepsearch/pkg/di"
)

func SetupSearch(ctx context.Context) error {
	if e := di.Provide(ctx,
		func(ctx context.Context) (*search.Service, error) {
			env := di.MustInvoke[Env](ctx)
			llm := di.MustInvoke[LLM](ctx)

			maxUses := env.WebSearchMaxUses
			if maxUses <= 0 {
				maxUses = 5
			}

			return search.New(ctx, llm, search.Config{
				Model:        env.Model,
				OutputLimit:  env.ModelOutputLimit,
				MaxUses:      maxUses,
				FetchEnabled: env.WebFetchEnabled,
				Fetch: tools.WebFetchConfig{
					Timeout:  env.WebFetchTimeout,
					MaxBytes: env.WebFetchMaxBytes,
					MaxChars: env.WebFetchMaxChars,
				},
			})
		},
	); e != nil {
		return fmt.Errorf("failed to provide search service: %w", e)
	}

	return nil
}
