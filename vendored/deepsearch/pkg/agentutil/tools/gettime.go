package tools

import (
	"context"
	"time"

	"charm.land/fantasy"
)

func NewGetTimeTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"GetTime",
		"Get the current date and time in Asia/Shanghai (UTC+8), returned as RFC3339 string. "+
			"Call this tool whenever you need the exact time, because the system prompt only contains today's date.",
		func(ctx context.Context, i struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			location := time.FixedZone("CST", 60*60*8)
			return fantasy.ToolResponse{
				Content: time.Now().In(location).Format(time.RFC3339),
			}, nil
		},
	)
}
