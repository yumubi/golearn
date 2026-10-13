package search

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
)

func TestSearchRejectsEmptyQuery(t *testing.T) {
	service := &Service{}

	if _, e := service.Search(context.Background(), "   "); !errors.Is(e, ErrEmptyQuery) {
		t.Fatalf("error = %v, want %v", e, ErrEmptyQuery)
	}
}

func TestFetchChecksConfigurationAndAddress(t *testing.T) {
	disabled := &Service{cfg: Config{}}
	if _, e := disabled.Fetch(context.Background(), "https://example.com"); !errors.Is(e, ErrFetchDisabled) {
		t.Fatalf("disabled error = %v, want %v", e, ErrFetchDisabled)
	}

	enabled := &Service{cfg: Config{FetchEnabled: true}}
	if _, e := enabled.Fetch(context.Background(), "http://127.0.0.1/"); !errors.Is(e, ErrInvalidURL) {
		t.Fatalf("invalid url error = %v, want %v", e, ErrInvalidURL)
	}
}

func TestCollectSourcesDeduplicatesAndKeepsOrder(t *testing.T) {
	run := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				source("https://a.example", "A"),
				source("https://a.example", "A again"),
				source("", "no url"),
			}}},
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "intermediate"},
				source("https://b.example", "B"),
			}}},
		},
		Response: fantasy.Response{Content: fantasy.ResponseContent{
			fantasy.TextContent{Text: "final"},
			source("https://b.example", "B again"),
		}},
	}

	sources := collectSources(run)
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want 2 entries", sources)
	}
	if sources[0].URL != "https://a.example" || sources[0].Title != "A" {
		t.Fatalf("first source = %+v", sources[0])
	}
	if sources[1].URL != "https://b.example" || sources[1].Title != "B" {
		t.Fatalf("second source = %+v", sources[1])
	}
}

func source(url, title string) fantasy.SourceContent {
	return fantasy.SourceContent{SourceType: fantasy.SourceTypeURL, ID: url, URL: url, Title: title}
}
