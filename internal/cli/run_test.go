package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/hn"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

type fakeHNClient struct {
	comments []hn.Comment
	err      error
}

func (f *fakeHNClient) FetchThread(ctx context.Context, threadID string) ([]hn.Comment, error) {
	return f.comments, f.err
}

type fakeHNClientFunc struct {
	fetch func(ctx context.Context, threadID string) ([]hn.Comment, error)
}

func (f *fakeHNClientFunc) FetchThread(ctx context.Context, threadID string) ([]hn.Comment, error) {
	return f.fetch(ctx, threadID)
}

// fakeExtractor maps a comment's raw text to a canned JobPosting, or to a
// simulated failure if the text is listed in failTexts.
type fakeExtractor struct {
	results   map[string]job.JobPosting
	failTexts map[string]bool
}

func (f *fakeExtractor) ExtractJob(ctx context.Context, commentText string) (job.JobPosting, error) {
	if f.failTexts[commentText] {
		return job.JobPosting{}, errors.New("simulated extraction failure")
	}
	return f.results[commentText], nil
}

type fakeConverter struct{}

func (fakeConverter) ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error) {
	return amount * 2, nil
}

// failingConverter simulates an FX conversion that always fails, e.g.
// because rates could not be fetched at startup.
type failingConverter struct {
	err error
}

func (f failingConverter) ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error) {
	return 0, f.err
}

func TestRun_WritesCSVForAllExtractedJobs(t *testing.T) {
	comments := []hn.Comment{
		{ID: 1, Text: "Company A"},
		{ID: 2, Text: "Company B"},
	}
	results := map[string]job.JobPosting{
		"Company A": {Location: "Remote", JobTitle: "Engineer A", HasSalary: false},
		"Company B": {Location: "Onsite", JobTitle: "Engineer B", HasSalary: true, SalaryMinAmount: 100, SalaryMaxAmount: 200, SalaryCurrencyCode: "EUR"},
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")

	summary, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 2}, Deps{
		HN:        &fakeHNClient{comments: comments},
		Extractor: &fakeExtractor{results: results, failTexts: map[string]bool{}},
		Converter: fakeConverter{},
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if summary.Written != 2 || summary.Skipped != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output CSV: %v", err)
	}
	if !strings.Contains(string(content), "Engineer A") || !strings.Contains(string(content), "Engineer B") {
		t.Errorf("CSV missing expected rows: %s", content)
	}
	if !strings.Contains(string(content), "300") { // (100+200)/2 * 2 = 300
		t.Errorf("CSV missing normalized salary: %s", content)
	}
}

func TestRun_SkipsFailedExtractionsButWritesTheRest(t *testing.T) {
	comments := []hn.Comment{
		{ID: 1, Text: "Good Comment"},
		{ID: 2, Text: "Bad Comment"},
	}
	results := map[string]job.JobPosting{
		"Good Comment": {Location: "Remote", JobTitle: "Engineer"},
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")
	var stderr bytes.Buffer

	summary, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 2}, Deps{
		HN:        &fakeHNClient{comments: comments},
		Extractor: &fakeExtractor{results: results, failTexts: map[string]bool{"Bad Comment": true}},
		Converter: fakeConverter{},
	}, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if summary.Written != 1 || summary.Skipped != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !strings.Contains(stderr.String(), "skipping comment") {
		t.Errorf("expected a warning about the skipped comment, got: %s", stderr.String())
	}
}

func TestRun_ReturnsErrorWhenAllExtractionsFail(t *testing.T) {
	comments := []hn.Comment{{ID: 1, Text: "Bad Comment"}}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")

	_, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 1}, Deps{
		HN:        &fakeHNClient{comments: comments},
		Extractor: &fakeExtractor{results: map[string]job.JobPosting{}, failTexts: map[string]bool{"Bad Comment": true}},
		Converter: fakeConverter{},
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when every comment fails extraction, got nil")
	}
}

func TestRun_FetchThreadFailureIsFatal(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")

	_, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 1}, Deps{
		HN:        &fakeHNClient{err: errors.New("network down")},
		Extractor: &fakeExtractor{},
		Converter: fakeConverter{},
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when fetching the thread fails, got nil")
	}
}

func TestRun_InvalidURLIsRejectedBeforeFetching(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")

	fetchCalled := false
	hnClient := &fakeHNClientFunc{
		fetch: func(ctx context.Context, threadID string) ([]hn.Comment, error) {
			fetchCalled = true
			return nil, nil
		},
	}

	_, err := Run(context.Background(), Config{ThreadURL: "not a url or id", OutPath: outPath}, Deps{
		HN:        hnClient,
		Extractor: &fakeExtractor{},
		Converter: fakeConverter{},
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for an invalid --url, got nil")
	}
	if fetchCalled {
		t.Error("FetchThread should not be called when the URL is invalid")
	}
}

func TestRun_FXFailureLeavesNormalizedUSDEmpty(t *testing.T) {
	comments := []hn.Comment{
		{ID: 1, Text: "Company A"},
	}
	results := map[string]job.JobPosting{
		"Company A": {
			Location:           "Remote",
			JobTitle:           "Engineer A",
			HasSalary:          true,
			SalaryMinAmount:    100,
			SalaryMaxAmount:    200,
			SalaryCurrencyCode: "EUR",
		},
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")
	var stderr bytes.Buffer

	summary, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 2}, Deps{
		HN:        &fakeHNClient{comments: comments},
		Extractor: &fakeExtractor{results: results, failTexts: map[string]bool{}},
		Converter: failingConverter{err: errors.New("FX rates unavailable: fetch failed")},
	}, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if summary.Written != 1 || summary.Skipped != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output CSV: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 data row, got: %q", content)
	}
	fields := strings.Split(lines[1], ",")
	// location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd
	if fields[5] != "100" || fields[6] != "200" || fields[7] != "EUR" {
		t.Errorf("expected min/max/currency to still be populated, got row: %q", lines[1])
	}
	if fields[8] != "" {
		t.Errorf("expected salary_normalized_usd to be empty on FX failure, got: %q", fields[8])
	}
}

func TestRun_FXWarningIsDeduplicatedAcrossComments(t *testing.T) {
	comments := []hn.Comment{
		{ID: 1, Text: "Company A"},
		{ID: 2, Text: "Company B"},
		{ID: 3, Text: "Company C"},
	}
	results := map[string]job.JobPosting{
		"Company A": {Location: "Remote", JobTitle: "Engineer A", HasSalary: true, SalaryMinAmount: 100, SalaryMaxAmount: 200, SalaryCurrencyCode: "EUR"},
		"Company B": {Location: "Remote", JobTitle: "Engineer B", HasSalary: true, SalaryMinAmount: 50, SalaryMaxAmount: 90, SalaryCurrencyCode: "GBP"},
		"Company C": {Location: "Remote", JobTitle: "Engineer C", HasSalary: true, SalaryMinAmount: 10, SalaryMaxAmount: 20, SalaryCurrencyCode: "CHF"},
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "jobs.csv")
	var stderr bytes.Buffer

	sameErr := errors.New("FX rates unavailable: fetch failed")
	summary, err := Run(context.Background(), Config{ThreadURL: "12345", OutPath: outPath, Concurrency: 3}, Deps{
		HN:        &fakeHNClient{comments: comments},
		Extractor: &fakeExtractor{results: results, failTexts: map[string]bool{}},
		Converter: failingConverter{err: sameErr},
	}, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if summary.Written != 3 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	warningText := "could not normalize salary to USD: " + sameErr.Error()
	count := strings.Count(stderr.String(), warningText)
	if count != 1 {
		t.Errorf("expected the FX warning to appear exactly once, got %d times in stderr: %s", count, stderr.String())
	}
}
