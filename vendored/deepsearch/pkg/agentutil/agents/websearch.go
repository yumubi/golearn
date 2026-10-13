package agents

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"

	"deepsearch/pkg/agentutil/prompt"
	"deepsearch/pkg/agentutil/tools"
)

// WebSearchConfig carries the runtime configuration of the WebSearch agent.
type WebSearchConfig struct {
	Provider    LanguageModelProvider
	Model       string
	OutputLimit int64
	MaxUses     int64
	// FetchEnabled registers the WebFetch tool, which reads one public web
	// page per call with local and reserved networks blocked.
	FetchEnabled bool
	// Fetch tunes the WebFetch tool; zero values use its defaults.
	Fetch tools.WebFetchConfig
}

func NewWebSearchAgent(ctx context.Context, cfg WebSearchConfig) (fantasy.Agent, error) {
	model, err := cfg.Provider.LanguageModel(ctx, cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}

	systemPrompt := strings.Join([]string{
		prompt.BaseModelPrompt,
		prompt.WebSearchAgentSystemPrompt,
		prompt.DatePrompt(),
	}, "\n\n")

	options := []fantasy.AgentOption{
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithProviderDefinedTools(
			anthropic.WebSearchTool(&anthropic.WebSearchToolOptions{
				MaxUses: cfg.MaxUses,
			}),
		),
		fantasy.WithTools(webSearchTools(cfg)...),
	}

	if cfg.OutputLimit > 0 {
		options = append(options, fantasy.WithMaxOutputTokens(cfg.OutputLimit))
	}

	return fantasy.NewAgent(
		model,
		options...,
	), nil
}

// webSearchTools assembles the tools of the WebSearch agent.
func webSearchTools(cfg WebSearchConfig) []fantasy.AgentTool {
	agentTools := []fantasy.AgentTool{tools.NewGetTimeTool()}
	if cfg.FetchEnabled {
		agentTools = append(agentTools, tools.NewWebFetchTool(cfg.Fetch))
	}

	return agentTools
}
