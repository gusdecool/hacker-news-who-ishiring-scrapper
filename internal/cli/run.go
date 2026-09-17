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

// progress serializes stderr writes from concurrent extraction workers so
// progress lines don't interleave, and tracks how many comments are done.
type progress struct {
	mu     sync.Mutex
	done   int
	total  int
	stderr io.Writer
}

func (p *progress) reportDone(commentID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done++
	fmt.Fprintf(p.stderr, "[%d/%d] processed comment %d\n", p.done, p.total, commentID)
}

// batchSize is how many comments are sent to the extractor in a single
// call. Fixed rather than configurable: it only trades off prompt size
// against blast radius on a batch-level failure, not something worth
// exposing as a flag.
const batchSize = 20

// Config holds the run's non-credential settings. API keys are resolved
// into a JobExtractor by the caller before Run is invoked.
type Config struct {
	ThreadURL        string
	OutPath          string
	BatchConcurrency int
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

// chunkComments splits comments into contiguous groups of at most size,
// preserving order — the batch results returned for each group line up
// index-for-index with comments[i*size:], so offsets stay simple.
func chunkComments(comments []hn.Comment, size int) [][]hn.Comment {
	var chunks [][]hn.Comment
	for i := 0; i < len(comments); i += size {
		chunks = append(chunks, comments[i:min(i+size, len(comments))])
	}
	return chunks
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
	fmt.Fprintf(stderr, "fetched thread: %d comments\n", len(comments))

	concurrency := max(cfg.BatchConcurrency, 1)

	prog := &progress{total: len(comments), stderr: stderr}

	type result struct {
		posting job.JobPosting
		err     error
		warning string
	}

	results := make([]result, len(comments))
	batches := chunkComments(comments, batchSize)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for bi, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(offset int, batch []hn.Comment) {
			defer wg.Done()
			defer func() { <-sem }()

			inputs := make([]extract.CommentInput, len(batch))
			for j, c := range batch {
				inputs[j] = extract.CommentInput{ID: c.ID, Text: c.Text}
			}

			batchResults, err := deps.Extractor.ExtractJobs(ctx, inputs)
			if err != nil {
				for j, c := range batch {
					results[offset+j] = result{err: fmt.Errorf("comment %d: %w", c.ID, err)}
					prog.reportDone(c.ID)
				}
				return
			}

			for j, r := range batchResults {
				if r.Err != nil {
					results[offset+j] = result{err: r.Err}
					prog.reportDone(r.CommentID)
					continue
				}

				posting := r.Posting
				var warning string
				if posting.HasSalary {
					usd, convErr := deps.Converter.ToUSD(ctx, (posting.SalaryMinAmount+posting.SalaryMaxAmount)/2, posting.SalaryCurrencyCode)
					if convErr != nil {
						warning = fmt.Sprintf("could not normalize salary to USD: %v", convErr)
					} else {
						posting.SalaryNormalizedUSD = usd
						posting.HasNormalizedUSD = true
					}
				}

				results[offset+j] = result{posting: posting, warning: warning}
				prog.reportDone(r.CommentID)
			}
		}(bi*batchSize, batch)
	}
	wg.Wait()

	var jobs []job.JobPosting
	skipped := 0
	printedWarnings := make(map[string]bool)
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(stderr, "warning: skipping comment: %v\n", r.err)
			skipped++
			continue
		}
		if r.warning != "" && !printedWarnings[r.warning] {
			printedWarnings[r.warning] = true
			fmt.Fprintf(stderr, "warning: %s\n", r.warning)
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
