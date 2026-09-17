package extract

import (
	"context"
	"fmt"
	"strings"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

// JobExtractor turns a single HN comment's raw text into a structured job
// posting.
type JobExtractor interface {
	ExtractJob(ctx context.Context, commentText string) (job.JobPosting, error)
}

// Config holds every provider's credentials. A provider is selected by
// which of its fields is set — there is no separate selector flag.
type Config struct {
	GeminiAPIKey string
}

type providerConfig struct {
	name   string
	detect func(cfg Config) bool
	build  func(cfg Config) (JobExtractor, error)
}

var providers = []providerConfig{
	{
		name:   "gemini",
		detect: func(c Config) bool { return c.GeminiAPIKey != "" },
		build:  func(c Config) (JobExtractor, error) { return newGeminiExtractor(c.GeminiAPIKey) },
	},
}

// ResolveExtractor picks the one configured provider. It is a fatal error
// to configure zero or more than one.
func ResolveExtractor(cfg Config) (JobExtractor, error) {
	var matchedNames []string
	var extractor JobExtractor

	for _, p := range providers {
		if !p.detect(cfg) {
			continue
		}
		built, err := p.build(cfg)
		if err != nil {
			return nil, fmt.Errorf("configuring %s provider: %w", p.name, err)
		}
		matchedNames = append(matchedNames, p.name)
		extractor = built
	}

	switch len(matchedNames) {
	case 0:
		return nil, fmt.Errorf("no LLM provider configured: set --gemini-api-key or GEMINI_API_KEY")
	case 1:
		return extractor, nil
	default:
		return nil, fmt.Errorf("multiple LLM providers configured (%s): unset all but one", strings.Join(matchedNames, ", "))
	}
}
