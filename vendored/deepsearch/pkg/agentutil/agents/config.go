package agents

import (
	"context"

	"charm.land/fantasy"
)

// LanguageModelProvider loads a fantasy language model by id.
type LanguageModelProvider interface {
	LanguageModel(ctx context.Context, modelID string) (fantasy.LanguageModel, error)
}
