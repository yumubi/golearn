package provider

import (
	"context"
	"fmt"
	"log/slog"

	"deepsearch/pkg/di"
	"deepsearch/pkg/util"

	env_struct "deepsearch/pkg/env"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

const EnvConfPrefix = "APP_"

type DotenvPathCtx struct{}

type Env struct{ *env_struct.Env }

func SetupEnv(ctx context.Context) error {
	if e := di.Provide(ctx,
		func(ctx context.Context) (Env, error) {
			envData := &env_struct.Env{}

			if dotenvPath, ok := ctx.Value(DotenvPathCtx{}).(string); ok &&
				dotenvPath != "" && util.Exists(dotenvPath) {
				slog.Info("loading env from dotenv file", "path", dotenvPath)

				dotenv, e := godotenv.Read(dotenvPath)
				if e != nil {
					return Env{}, fmt.Errorf("read dotenv file: %w", e)
				}

				if e := env.ParseWithOptions(envData, env.Options{
					Environment: dotenv,
				}); e != nil {
					return Env{}, fmt.Errorf("parse dotenv file: %w", e)
				}

				return Env{envData}, nil
			}

			slog.Info("loading env from environment variables")

			if e := env.ParseWithOptions(envData, env.Options{
				Prefix: EnvConfPrefix,
			}); e != nil {
				return Env{}, fmt.Errorf("parse env: %w", e)
			}

			return Env{envData}, nil
		},
		di.WithCleanRecursive(true),
	); e != nil {
		return fmt.Errorf("failed to provide env: %w", e)
	}

	return nil
}
