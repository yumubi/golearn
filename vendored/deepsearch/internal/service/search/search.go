package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"

	"deepsearch/pkg/agentutil/agents"
	"deepsearch/pkg/agentutil/tools"
)

var (
	// ErrEmptyQuery is returned when a search request carries no query.
	ErrEmptyQuery = errors.New("query is required")
	// ErrInvalidURL is returned when WebFetch refuses the requested address.
	ErrInvalidURL = errors.New("url is not readable")
	// ErrFetchFailed is returned when the requested page could not be read.
	ErrFetchFailed = errors.New("page could not be read")
	// ErrFetchDisabled is returned when WebFetch is turned off by configuration.
	ErrFetchDisabled = errors.New("web fetch is disabled")
)

// Source is one web page a search answer cites.
type Source struct {
	URL   string `json:"url" jsonschema:"URL of the cited page"`
	Title string `json:"title" jsonschema:"title of the cited page"`
}

// Usage is the token usage of one search.
type Usage struct {
	InputTokens         int64 `json:"input_tokens" jsonschema:"prompt tokens sent to the model"`
	OutputTokens        int64 `json:"output_tokens" jsonschema:"completion tokens produced by the model"`
	TotalTokens         int64 `json:"total_tokens" jsonschema:"total tokens of this search"`
	ReasoningTokens     int64 `json:"reasoning_tokens" jsonschema:"tokens spent on model reasoning"`
	CacheCreationTokens int64 `json:"cache_creation_tokens" jsonschema:"prompt tokens written to the provider cache"`
	CacheReadTokens     int64 `json:"cache_read_tokens" jsonschema:"prompt tokens read from the provider cache"`
}

// Result is the complete answer of one search.
type Result struct {
	Answer     string   `json:"answer" jsonschema:"concise answer with inline or trailing source URLs"`
	Sources    []Source `json:"sources" jsonschema:"web pages the answer cites"`
	Usage      Usage    `json:"usage" jsonschema:"token usage of this search"`
	Model      string   `json:"model" jsonschema:"model that produced the answer"`
	DurationMS int64    `json:"duration_ms" jsonschema:"wall-clock duration of the search in milliseconds"`
}

// Link is one outgoing link of a fetched page.
type Link struct {
	URL  string `json:"url" jsonschema:"absolute link target"`
	Text string `json:"text" jsonschema:"link text"`
}

// Page is one public web page read by WebFetch.
type Page struct {
	URL       string `json:"url" jsonschema:"final URL after redirects"`
	Title     string `json:"title" jsonschema:"page title"`
	Text      string `json:"text" jsonschema:"visible page text"`
	Links     []Link `json:"links" jsonschema:"outgoing links of the page"`
	Truncated bool   `json:"truncated" jsonschema:"whether the page was longer than the read limit"`
}

// Config carries the runtime configuration of the search service.
type Config struct {
	Model        string
	OutputLimit  int64
	MaxUses      int64
	FetchEnabled bool
	Fetch        tools.WebFetchConfig
}

// Service answers web searches and reads single public web pages.
type Service struct {
	cfg   Config
	agent fantasy.Agent
}

func New(ctx context.Context, provider agents.LanguageModelProvider, cfg Config) (*Service, error) {
	agent, e := agents.NewWebSearchAgent(ctx, agents.WebSearchConfig{
		Provider:     provider,
		Model:        cfg.Model,
		OutputLimit:  cfg.OutputLimit,
		MaxUses:      cfg.MaxUses,
		FetchEnabled: cfg.FetchEnabled,
		Fetch:        cfg.Fetch,
	})
	if e != nil {
		return nil, fmt.Errorf("build search agent: %w", e)
	}

	return &Service{cfg: cfg, agent: agent}, nil
}

// Search answers one query with web results and the pages it cited.
func (s *Service) Search(ctx context.Context, query string) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, ErrEmptyQuery
	}

	started := time.Now()
	// The Anthropic web_search server tool only runs on streamed requests, so
	// the agent streams internally even though this API answers synchronously.
	run, e := s.agent.Stream(ctx, fantasy.AgentStreamCall{Prompt: query})
	if e != nil {
		return Result{}, fmt.Errorf("run search agent: %w", e)
	}

	return Result{
		Answer:     run.Response.Content.Text(),
		Sources:    collectSources(run),
		Usage:      usageOf(run.TotalUsage),
		Model:      s.cfg.Model,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

// Fetch reads one public web page and returns its title, text and links.
func (s *Service) Fetch(ctx context.Context, rawURL string) (Page, error) {
	if !s.cfg.FetchEnabled {
		return Page{}, ErrFetchDisabled
	}
	if e := tools.CheckFetchURL(rawURL); e != nil {
		return Page{}, fmt.Errorf("%w: %s", ErrInvalidURL, e)
	}

	page, e := tools.FetchPage(ctx, s.cfg.Fetch, rawURL)
	if e != nil {
		return Page{}, fmt.Errorf("%w: %s", ErrFetchFailed, e)
	}

	links := make([]Link, 0, len(page.Links))
	for _, link := range page.Links {
		links = append(links, Link{URL: link.URL, Text: link.Text})
	}

	return Page{
		URL:       page.URL,
		Title:     page.Title,
		Text:      page.Text,
		Links:     links,
		Truncated: page.Truncated,
	}, nil
}

// collectSources lists the pages every step of the run searched or read,
// deduplicated by URL and in the order the agent first saw them.
func collectSources(run *fantasy.AgentResult) []Source {
	sources := []Source{}
	seen := map[string]bool{}

	add := func(content fantasy.ResponseContent) {
		for _, source := range content.Sources() {
			url := strings.TrimSpace(source.URL)
			if url == "" || seen[url] {
				continue
			}

			seen[url] = true
			sources = append(sources, Source{URL: url, Title: source.Title})
		}
	}

	for _, step := range run.Steps {
		add(step.Content)
	}
	add(run.Response.Content)

	return sources
}

func usageOf(usage fantasy.Usage) Usage {
	return Usage{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		TotalTokens:         usage.TotalTokens,
		ReasoningTokens:     usage.ReasoningTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		CacheReadTokens:     usage.CacheReadTokens,
	}
}
