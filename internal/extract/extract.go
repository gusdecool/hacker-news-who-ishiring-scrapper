package extract

import (
	"context"
	"fmt"
	"strings"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

// CommentInput is one comment to extract a job posting from. ID is echoed
// back in JobResult so batched results can be matched to their comment
// regardless of the order an LLM returns them in.
type CommentInput struct {
	ID   int
	Text string
}

// JobResult is one comment's extraction outcome within a batch. Err is set
// (with Posting left zero) when only this comment's extraction failed —
// e.g. it was missing from the model's response — without that failing the
// rest of the batch.
type JobResult struct {
	CommentID int
	Posting   job.JobPosting
	Err       error
}

// JobExtractor turns a batch of HN comments' raw text into structured job
// postings. A non-nil returned error means the whole batch failed (e.g. the
// request itself errored); a nil error with per-item Err set means only
// those comments failed.
type JobExtractor interface {
	ExtractJobs(ctx context.Context, comments []CommentInput) ([]JobResult, error)
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
