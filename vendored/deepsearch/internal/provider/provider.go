package provider

import (
	"context"
	"fmt"

	"deepsearch/pkg/di"
	"deepsearch/pkg/util"
)

func Setup(ctx context.Context) error {
	if e := (util.SetupHandlers{
		SetupEnv,
		SetupLogger,
		SetupLLM,
		SetupSearch,
		SetupHttp,
	}).Setup(ctx); e != nil {
		return fmt.Errorf("setup provider: %w", e)
	}

	if _, e := di.Invoke[Logger](ctx); e != nil {
		return fmt.Errorf("failed to invoke logger: %w", e)
	}

	return nil
}
