package cli

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/extract"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/fx"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/hn"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

// Config holds the run's non-credential settings. API keys are resolved
// into a JobExtractor by the caller before Run is invoked.
type Config struct {
	ThreadURL   string
	OutPath     string
	Concurrency int
}

// Deps wires the collaborators Run needs. Tests supply fakes for all three.
type Deps struct {
	HN        hn.Client
	Extractor extract.JobExtractor
	Converter fx.CurrencyConverter
}

// Summary reports how many jobs were written vs. skipped due to errors.
type Summary struct {
	Written int
	Skipped int
}

// Run fetches the thread, extracts a JobPosting per top-level comment,
// normalizes any stated salary to USD, and writes the results to
// cfg.OutPath as CSV.
//
// A per-comment extraction failure is logged to stderr and that comment is
// skipped; Run only returns an error if zero jobs were extracted overall,
// the --url is invalid, fetching the thread fails, or writing the CSV
// fails.
func Run(ctx context.Context, cfg Config, deps Deps, stderr io.Writer) (Summary, error) {
	threadID, err := hn.ParseThreadID(cfg.ThreadURL)
	if err != nil {
		return Summary{}, fmt.Errorf("invalid --url: %w", err)
	}

	comments, err := deps.HN.FetchThread(ctx, threadID)
	if err != nil {
		return Summary{}, fmt.Errorf("fetching thread: %w", err)
	}

	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	type result struct {
		posting job.JobPosting
		err     error
	}

	results := make([]result, len(comments))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, c := range comments {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c hn.Comment) {
			defer wg.Done()
			defer func() { <-sem }()

			posting, err := deps.Extractor.ExtractJob(ctx, c.Text)
			if err != nil {
				results[i] = result{err: fmt.Errorf("comment %d: %w", c.ID, err)}
				return
			}

			if posting.HasSalary {
				usd, convErr := deps.Converter.ToUSD(ctx, (posting.SalaryMinAmount+posting.SalaryMaxAmount)/2, posting.SalaryCurrencyCode)
				if convErr != nil {
					fmt.Fprintf(stderr, "warning: could not normalize salary for comment %d: %v\n", c.ID, convErr)
				} else {
					posting.SalaryNormalizedUSD = usd
				}
			}

			results[i] = result{posting: posting}
		}(i, c)
	}
	wg.Wait()

	var jobs []job.JobPosting
	skipped := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(stderr, "warning: skipping comment: %v\n", r.err)
			skipped++
			continue
		}
		jobs = append(jobs, r.posting)
	}

	if len(jobs) == 0 {
		return Summary{Skipped: skipped}, fmt.Errorf("no jobs extracted (%d comments failed)", skipped)
	}

	if err := job.WriteCSV(cfg.OutPath, jobs); err != nil {
		return Summary{}, fmt.Errorf("writing csv: %w", err)
	}

	summary := Summary{Written: len(jobs), Skipped: skipped}
	fmt.Fprintf(stderr, "%d jobs written, %d skipped (errors)\n", summary.Written, summary.Skipped)
	return summary, nil
}
