package prompt

import (
	_ "embed"
)

var (
	//go:embed base_model_prompt.md
	BaseModelPrompt string

	//go:embed websearch_agent_system_prompt.md
	WebSearchAgentSystemPrompt string
)
