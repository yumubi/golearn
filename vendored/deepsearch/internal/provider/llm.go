package provider

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"

	"deepsearch/pkg/di"

	"github.com/puzpuzpuz/xsync/v4"
)

type llmModelEntry struct {
	model fantasy.LanguageModel
	err   error
}

type LLM struct {
	Provider fantasy.Provider
	models   *xsync.Map[string, *llmModelEntry]
}

func NewLLM(p fantasy.Provider) LLM {
	return LLM{
		Provider: p,
		models:   xsync.NewMap[string, *llmModelEntry](),
	}
}

func (l LLM) LanguageModel(ctx context.Context, modelId string) (fantasy.LanguageModel, error) {
	entry, _ := l.models.LoadOrCompute(modelId, func() (*llmModelEntry, bool) {
		model, err := l.Provider.LanguageModel(ctx, modelId)
		return &llmModelEntry{model, err}, false
	})
	if entry.err != nil {
		return nil, fmt.Errorf("create language model %q: %w", modelId, entry.err)
	}
	return entry.model, nil
}

func SetupLLM(ctx context.Context) error {
	if e := di.Provide(ctx,
		func(ctx context.Context) (LLM, error) {
			env := di.MustInvoke[Env](ctx)
			if env.ModelApiKey == "" {
				return LLM{}, fmt.Errorf("MODEL_API_KEY is not set")
			}
			if env.Model == "" {
				return LLM{}, fmt.Errorf("MODEL is not set")
			}

			p, e := anthropic.New(
				anthropic.WithAPIKey(env.ModelApiKey),
				anthropic.WithBaseURL(env.ModelBaseURL),
			)
			if e != nil {
				return LLM{}, fmt.Errorf("create model provider: %w", e)
			}

			return NewLLM(p), nil
		},
	); e != nil {
		return fmt.Errorf("failed to provide llm: %w", e)
	}

	return nil
}
