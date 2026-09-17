# HN "Who is Hiring" Scraper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go CLI (`hnwih`) that scrapes an "Ask HN: Who is hiring?" thread's top-level comments into a structured, salary-normalized CSV file.

**Architecture:** Four small, independently testable `internal` packages (`job`, `hn`, `fx`, `extract`) provide data model/CSV, HN fetching, currency conversion, and LLM extraction respectively, each behind a narrow interface. A fifth package, `internal/cli`, orchestrates them (bounded-concurrency worker pool, error skip-and-continue) behind a single exported `Run` function so the orchestration logic is testable with fakes without touching `os.Args` or real network calls. `cmd/hnwih/main.go` is a thin cobra-based entrypoint that resolves flags/env vars, builds the real dependencies, and calls `cli.Run`.

**Tech Stack:** Go 1.24+, `google.golang.org/genai` (Gemini structured output), `github.com/spf13/cobra` (CLI flags), HN Algolia API, Frankfurter FX API (both via stdlib `net/http`, no client library).

**Spec:** [docs/superpowers/specs/2026-09-17-hn-job-scraper-design.md](../specs/2026-09-17-hn-job-scraper-design.md)

## Global Constraints

- Module path: `github.com/gusdecool/hacker-news-who-ishiring-scrapper`; `go.mod` declares `go 1.24` (floor imposed by `google.golang.org/genai`, which requires 1.24).
- No `--llm-provider` flag. The provider is inferred from which provider-specific API key is set; exactly one must be set or it's a fatal config error.
- Only the thread's **direct top-level comments** are job postings. The root story post's own text (guidelines) and nested replies are never treated as job data.
- No persistent cross-run FX caching — one in-memory rate table fetch per process run, reused via a local map lookup for every `ToUSD` call (`ToUSD` itself never makes an HTTP request).
- CSV column order is exactly: `location, job_title, description, how_to_apply, salary_actual, salary_min_amount, salary_max_amount, salary_currency_code, salary_normalized_usd`.
- Salary columns (`salary_min_amount`, `salary_max_amount`, `salary_currency_code`, `salary_normalized_usd`) are written as **empty strings**, never `"0"`, when a job has no stated salary.
- Error handling: HN thread fetch failure is fatal; a single comment's extraction failure is logged and skipped (fatal only if *zero* jobs end up extracted); FX rate fetch failure is a non-fatal warning that leaves salary-normalization columns empty.
- Config resolution order: CLI flag > environment variable > fatal error if a required value is missing.
- No filtering subcommand, no nested-reply processing, no database — a single CSV file per run (all v1 non-goals from the spec).

---

## Task 1: `internal/job` — shared schema and CSV writer

**Files:**
- Create: `go.mod` (module init)
- Create: `internal/job/job.go`
- Test: `internal/job/job_test.go`

**Interfaces:**
- Consumes: nothing (first package, no dependencies).
- Produces:
  - `type JobPosting struct { Location, JobTitle, Description, HowToApply, SalaryActual, SalaryCurrencyCode string; SalaryMinAmount, SalaryMaxAmount, SalaryNormalizedUSD float64; HasSalary bool }`
  - `func WriteCSV(path string, jobs []JobPosting) error`

- [ ] **Step 1: Initialize the Go module**

Run:
```bash
go mod init github.com/gusdecool/hacker-news-who-ishiring-scrapper
```

Then open the generated `go.mod` and change its `go` directive line to exactly:

```
go 1.24
```

- [ ] **Step 2: Write the failing tests**

Create `internal/job/job_test.go`:

```go
package job

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCSV_WithSalary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:            "Remote (Europe)",
			JobTitle:            "Senior Product Engineer",
			Description:         "Build cool things",
			HowToApply:          "https://modash.io",
			SalaryActual:        "€75k-110k",
			SalaryMinAmount:     75000,
			SalaryMaxAmount:     110000,
			SalaryCurrencyCode:  "EUR",
			SalaryNormalizedUSD: 106500.5,
			HasSalary:           true,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"Remote (Europe),Senior Product Engineer,Build cool things,https://modash.io,€75k-110k,75000,110000,EUR,106500.5\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}

func TestWriteCSV_WithoutSalary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:    "On-site San Francisco",
			JobTitle:    "Backend Engineer",
			Description: "No salary mentioned",
			HowToApply:  "jobs@example.com",
			HasSalary:   false,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"On-site San Francisco,Backend Engineer,No salary mentioned,jobs@example.com,,,,,\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}

func TestWriteCSV_EscapesSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:    "Remote",
			JobTitle:    "Engineer, Platform",
			Description: "Line one\nLine two",
			HowToApply:  "apply@example.com",
			HasSalary:   false,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"Remote,\"Engineer, Platform\",\"Line one\nLine two\",apply@example.com,,,,,\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/job/... -v`
Expected: FAIL — compile error, `JobPosting` and `WriteCSV` are undefined (package `job` has no non-test files yet).

- [ ] **Step 4: Implement `internal/job/job.go`**

```go
package job

import (
	"encoding/csv"
	"os"
	"strconv"
)

// JobPosting is one structured job record extracted from a single HN comment.
type JobPosting struct {
	Location            string
	JobTitle            string
	Description         string
	HowToApply          string
	SalaryActual        string
	SalaryMinAmount     float64
	SalaryMaxAmount     float64
	SalaryCurrencyCode  string
	SalaryNormalizedUSD float64
	HasSalary           bool
}

var csvHeader = []string{
	"location",
	"job_title",
	"description",
	"how_to_apply",
	"salary_actual",
	"salary_min_amount",
	"salary_max_amount",
	"salary_currency_code",
	"salary_normalized_usd",
}

// WriteCSV writes jobs to path as CSV with csvHeader as the header row.
// Salary fields are written as empty strings, not "0", when a job has no
// stated salary (JobPosting.HasSalary == false).
func WriteCSV(path string, jobs []JobPosting) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return err
	}

	for _, j := range jobs {
		if err := w.Write(j.row()); err != nil {
			return err
		}
	}

	w.Flush()
	return w.Error()
}

func (j JobPosting) row() []string {
	if !j.HasSalary {
		return []string{j.Location, j.JobTitle, j.Description, j.HowToApply, j.SalaryActual, "", "", "", ""}
	}
	return []string{
		j.Location,
		j.JobTitle,
		j.Description,
		j.HowToApply,
		j.SalaryActual,
		formatFloat(j.SalaryMinAmount),
		formatFloat(j.SalaryMaxAmount),
		j.SalaryCurrencyCode,
		formatFloat(j.SalaryNormalizedUSD),
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/job/... -v`
Expected: PASS (all three tests).

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/job
git commit -m "Add JobPosting schema and CSV writer"
```

---

## Task 2: `internal/hn` — HN thread fetching

**Files:**
- Create: `internal/hn/hn.go`
- Test: `internal/hn/hn_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:
  - `type Comment struct { ID int; Text string }`
  - `type Client interface { FetchThread(ctx context.Context, threadID string) ([]Comment, error) }`
  - `type AlgoliaClient struct { ... }` implementing `Client`
  - `func NewAlgoliaClient(httpClient *http.Client) *AlgoliaClient`
  - `func ParseThreadID(input string) (string, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/hn/hn_test.go`:

```go
package hn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchThread_FiltersDeadAndDeletedComments(t *testing.T) {
	body := `{
		"id": 49522897,
		"children": [
			{"id": 1, "author": "alice", "text": "Company A | Remote", "children": []},
			{"id": 2, "author": "", "text": "", "children": []},
			{"id": 3, "author": "bob", "text": "", "children": []},
			{"id": 4, "author": "carol", "text": "Company B | Onsite", "children": []}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := &AlgoliaClient{httpClient: server.Client(), baseURL: server.URL}

	comments, err := client.FetchThread(context.Background(), "49522897")
	if err != nil {
		t.Fatalf("FetchThread returned error: %v", err)
	}

	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2: %+v", len(comments), comments)
	}
	if comments[0].ID != 1 || comments[0].Text != "Company A | Remote" {
		t.Errorf("unexpected first comment: %+v", comments[0])
	}
	if comments[1].ID != 4 || comments[1].Text != "Company B | Onsite" {
		t.Errorf("unexpected second comment: %+v", comments[1])
	}
}

func TestFetchThread_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &AlgoliaClient{httpClient: server.Client(), baseURL: server.URL}

	if _, err := client.FetchThread(context.Background(), "49522897"); err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

func TestParseThreadID_NumericInput(t *testing.T) {
	id, err := ParseThreadID("49522897")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "49522897" {
		t.Errorf("got %q, want %q", id, "49522897")
	}
}

func TestParseThreadID_URLInput(t *testing.T) {
	id, err := ParseThreadID("https://news.ycombinator.com/item?id=49522897")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "49522897" {
		t.Errorf("got %q, want %q", id, "49522897")
	}
}

func TestParseThreadID_URLMissingID(t *testing.T) {
	if _, err := ParseThreadID("https://news.ycombinator.com/item"); err == nil {
		t.Fatal("expected error for URL missing ?id=, got nil")
	}
}

func TestParseThreadID_Empty(t *testing.T) {
	if _, err := ParseThreadID(""); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hn/... -v`
Expected: FAIL — compile error, `AlgoliaClient` and `ParseThreadID` are undefined.

- [ ] **Step 3: Implement `internal/hn/hn.go`**

```go
package hn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const defaultBaseURL = "https://hn.algolia.com/api/v1/items"

// Comment is one direct top-level reply in a "Who is hiring?" thread — one
// job posting.
type Comment struct {
	ID   int
	Text string
}

// Client fetches a thread's top-level comments.
type Client interface {
	FetchThread(ctx context.Context, threadID string) ([]Comment, error)
}

// AlgoliaClient fetches threads from the HN Algolia API.
type AlgoliaClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewAlgoliaClient builds a Client backed by the public HN Algolia API.
func NewAlgoliaClient(httpClient *http.Client) *AlgoliaClient {
	return &AlgoliaClient{httpClient: httpClient, baseURL: defaultBaseURL}
}

type algoliaItem struct {
	ID       int           `json:"id"`
	Author   string        `json:"author"`
	Text     string        `json:"text"`
	Children []algoliaItem `json:"children"`
}

// FetchThread fetches the thread identified by threadID (a numeric HN item
// ID) and returns its direct top-level comments, skipping any with no
// author or no text (dead/deleted/flagged). Nested replies are ignored:
// only the root item's direct children are job postings.
func (c *AlgoliaClient) FetchThread(ctx context.Context, threadID string) ([]Comment, error) {
	endpoint := c.baseURL + "/" + threadID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for thread %s: %w", threadID, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching thread %s: %w", threadID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching thread %s: unexpected status %d", threadID, resp.StatusCode)
	}

	var root algoliaItem
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, fmt.Errorf("decoding thread %s: %w", threadID, err)
	}

	comments := make([]Comment, 0, len(root.Children))
	for _, child := range root.Children {
		if child.Author == "" || child.Text == "" {
			continue
		}
		comments = append(comments, Comment{ID: child.ID, Text: child.Text})
	}
	return comments, nil
}

// ParseThreadID accepts either a raw numeric HN item ID or a full
// news.ycombinator.com/item?id=... URL and returns the numeric ID as a
// string.
func ParseThreadID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("thread URL or ID is required")
	}
	if _, err := strconv.Atoi(input); err == nil {
		return input, nil
	}

	u, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid URL or numeric thread ID", input)
	}
	id := u.Query().Get("id")
	if id == "" {
		return "", fmt.Errorf("%q does not look like an HN thread URL (missing ?id=)", input)
	}
	if _, err := strconv.Atoi(id); err != nil {
		return "", fmt.Errorf("thread id %q in URL is not numeric", id)
	}
	return id, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/hn/... -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/hn
git commit -m "Add HN Algolia client and thread ID parsing"
```

---

## Task 3: `internal/fx` — cached currency conversion

**Files:**
- Create: `internal/fx/fx.go`
- Test: `internal/fx/fx_test.go`

**Interfaces:**
- Consumes: nothing from Tasks 1–2.
- Produces:
  - `type CurrencyConverter interface { ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error) }`
  - `type FrankfurterConverter struct { ... }` implementing `CurrencyConverter`
  - `func NewFrankfurterConverter(httpClient *http.Client) *FrankfurterConverter`
  - `func (c *FrankfurterConverter) FetchRates(ctx context.Context) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/fx/fx_test.go`:

```go
package fx

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func newTestServer(t *testing.T, rates map[string]float64) (*httptest.Server, *int32) {
	t.Helper()
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"amount": 1.0,
			"base":   "USD",
			"rates":  rates,
		})
	}))
	t.Cleanup(server.Close)
	return server, &callCount
}

func TestToUSD_ConvertsUsingFetchedRatesAndCachesTheFetch(t *testing.T) {
	server, callCount := newTestServer(t, map[string]float64{"CHF": 0.8})
	c := &FrankfurterConverter{httpClient: server.Client(), baseURL: server.URL}

	if err := c.FetchRates(context.Background()); err != nil {
		t.Fatalf("FetchRates returned error: %v", err)
	}

	got, err := c.ToUSD(context.Background(), 80, "chf")
	if err != nil {
		t.Fatalf("ToUSD returned error: %v", err)
	}
	if math.Abs(got-100) > 0.0001 {
		t.Errorf("got %v, want ~100", got)
	}

	if _, err := c.ToUSD(context.Background(), 40, "CHF"); err != nil {
		t.Fatalf("second ToUSD call returned error: %v", err)
	}

	if got := atomic.LoadInt32(callCount); got != 1 {
		t.Errorf("got %d HTTP calls after FetchRates + 2 ToUSD calls, want exactly 1", got)
	}
}

func TestToUSD_USDIsIdentityEvenWithoutFetch(t *testing.T) {
	c := &FrankfurterConverter{httpClient: http.DefaultClient, baseURL: "http://unused.invalid"}

	got, err := c.ToUSD(context.Background(), 42, "USD")
	if err != nil {
		t.Fatalf("ToUSD returned error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %v, want 42", got)
	}
}

func TestToUSD_BeforeFetchReturnsError(t *testing.T) {
	c := &FrankfurterConverter{httpClient: http.DefaultClient, baseURL: "http://unused.invalid"}

	if _, err := c.ToUSD(context.Background(), 100, "EUR"); err == nil {
		t.Fatal("expected error before FetchRates is called, got nil")
	}
}

func TestToUSD_UnknownCurrency(t *testing.T) {
	server, _ := newTestServer(t, map[string]float64{"CHF": 0.8})
	c := &FrankfurterConverter{httpClient: server.Client(), baseURL: server.URL}

	if err := c.FetchRates(context.Background()); err != nil {
		t.Fatalf("FetchRates returned error: %v", err)
	}

	if _, err := c.ToUSD(context.Background(), 100, "XYZ"); err == nil {
		t.Fatal("expected error for unknown currency, got nil")
	}
}

func TestFetchRates_HTTPErrorIsReturnedByToUSD(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := &FrankfurterConverter{httpClient: server.Client(), baseURL: server.URL}

	if err := c.FetchRates(context.Background()); err == nil {
		t.Fatal("expected FetchRates to return an error, got nil")
	}

	if _, err := c.ToUSD(context.Background(), 100, "EUR"); err == nil {
		t.Fatal("expected ToUSD to return an error after a failed fetch, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/fx/... -v`
Expected: FAIL — compile error, `FrankfurterConverter` is undefined.

- [ ] **Step 3: Implement `internal/fx/fx.go`**

```go
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const defaultBaseURL = "https://api.frankfurter.dev/v1/latest?base=USD"

// CurrencyConverter converts an amount in a given ISO 4217 currency code to
// USD.
type CurrencyConverter interface {
	ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error)
}

type frankfurterResponse struct {
	Rates map[string]float64 `json:"rates"`
}

// FrankfurterConverter fetches the latest USD rate table once (via
// FetchRates) and serves every ToUSD call from that cached table — ToUSD
// never makes its own HTTP request.
type FrankfurterConverter struct {
	httpClient *http.Client
	baseURL    string

	mu       sync.Mutex
	rates    map[string]float64
	fetched  bool
	fetchErr error
}

// NewFrankfurterConverter builds a CurrencyConverter backed by the free
// Frankfurter FX rate API.
func NewFrankfurterConverter(httpClient *http.Client) *FrankfurterConverter {
	return &FrankfurterConverter{httpClient: httpClient, baseURL: defaultBaseURL}
}

// FetchRates fetches the full USD rate table once. Call it before any ToUSD
// call for a non-USD currency.
func (c *FrankfurterConverter) FetchRates(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		c.fetched = true
		c.fetchErr = fmt.Errorf("building FX rates request: %w", err)
		return c.fetchErr
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.fetched = true
		c.fetchErr = fmt.Errorf("fetching FX rates: %w", err)
		return c.fetchErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.fetched = true
		c.fetchErr = fmt.Errorf("fetching FX rates: unexpected status %d", resp.StatusCode)
		return c.fetchErr
	}

	var parsed frankfurterResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		c.fetched = true
		c.fetchErr = fmt.Errorf("decoding FX rates: %w", err)
		return c.fetchErr
	}

	c.rates = parsed.Rates
	c.fetched = true
	c.fetchErr = nil
	return nil
}

// ToUSD converts amount in currencyCode to USD using the table fetched by
// FetchRates. It never makes an HTTP request itself. Converting USD to USD
// always succeeds, even if FetchRates was never called or failed.
func (c *FrankfurterConverter) ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error) {
	code := strings.ToUpper(strings.TrimSpace(currencyCode))
	if code == "USD" {
		return amount, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.fetched {
		return 0, errors.New("FX rates not loaded: call FetchRates first")
	}
	if c.fetchErr != nil {
		return 0, fmt.Errorf("FX rates unavailable: %w", c.fetchErr)
	}

	rate, ok := c.rates[code]
	if !ok {
		return 0, fmt.Errorf("unknown currency code %q", code)
	}
	if rate == 0 {
		return 0, fmt.Errorf("invalid zero rate for currency %q", code)
	}

	return amount / rate, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/fx/... -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/fx
git commit -m "Add cached FX currency converter"
```

---

## Task 4: `internal/extract` — LLM extraction

**Files:**
- Create: `internal/extract/extract.go`
- Create: `internal/extract/gemini.go`
- Test: `internal/extract/extract_test.go`
- Test: `internal/extract/gemini_test.go`

**Interfaces:**
- Consumes: `job.JobPosting` (Task 1).
- Produces:
  - `type JobExtractor interface { ExtractJob(ctx context.Context, commentText string) (job.JobPosting, error) }`
  - `type Config struct { GeminiAPIKey string }`
  - `func ResolveExtractor(cfg Config) (JobExtractor, error)`
  - `type GeminiExtractor struct { ... }` implementing `JobExtractor`

- [ ] **Step 1: Add the Gemini SDK dependency**

Run:
```bash
go get google.golang.org/genai@latest
```

- [ ] **Step 2: Write the failing tests**

`extract.go` and `gemini.go` are mutually dependent for compilation (the
provider registry in `extract.go` calls `newGeminiExtractor` from
`gemini.go`), so both test files are written together before either
implementation file exists — the package won't compile until both land,
and testing after only one would compile is not possible.

Create `internal/extract/extract_test.go`:

```go
package extract

import "testing"

func TestResolveExtractor_NoProviderConfigured(t *testing.T) {
	if _, err := ResolveExtractor(Config{}); err == nil {
		t.Fatal("expected error when no provider is configured, got nil")
	}
}

func TestResolveExtractor_GeminiConfigured(t *testing.T) {
	extractor, err := ResolveExtractor(Config{GeminiAPIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extractor == nil {
		t.Fatal("expected a non-nil extractor")
	}
	if _, ok := extractor.(*GeminiExtractor); !ok {
		t.Fatalf("expected *GeminiExtractor, got %T", extractor)
	}
}
```

Create `internal/extract/gemini_test.go`:

```go
package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiExtractor_ExtractJob(t *testing.T) {
	var capturedBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)

		responseJSON := `{
			"location": "Remote (Europe)",
			"job_title": "Senior Product Engineer",
			"description": "Modash helps brands find creators.",
			"how_to_apply": "https://modash.io",
			"has_salary": true,
			"salary_actual": "€75k-110k",
			"salary_min_amount": 75000,
			"salary_max_amount": 110000,
			"salary_currency_code": "EUR"
		}`

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{"text": responseJSON},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	extractor, err := newGeminiExtractorWithOptions("test-key", server.URL, "test-model")
	if err != nil {
		t.Fatalf("newGeminiExtractorWithOptions returned error: %v", err)
	}

	got, err := extractor.ExtractJob(context.Background(), "Modash.io | Senior Product Engineer | Remote (Europe)")
	if err != nil {
		t.Fatalf("ExtractJob returned error: %v", err)
	}

	if got.JobTitle != "Senior Product Engineer" || got.Location != "Remote (Europe)" {
		t.Errorf("unexpected extraction result: %+v", got)
	}
	if !got.HasSalary || got.SalaryMinAmount != 75000 || got.SalaryMaxAmount != 110000 || got.SalaryCurrencyCode != "EUR" {
		t.Errorf("unexpected salary fields: %+v", got)
	}

	if !strings.Contains(capturedBody, `"responseMimeType":"application/json"`) {
		t.Errorf("request did not request structured JSON output, body: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, `"responseSchema"`) {
		t.Errorf("request did not include a response schema, body: %s", capturedBody)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/extract/... -v`
Expected: FAIL — compile error, `ResolveExtractor`, `Config`, `GeminiExtractor`, and `newGeminiExtractorWithOptions` are all undefined.

- [ ] **Step 4: Implement `internal/extract/extract.go`**

```go
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
```

- [ ] **Step 5: Implement `internal/extract/gemini.go`**

```go
package extract

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

const defaultGeminiModel = "gemini-flash-latest"

const extractionPrompt = `You are extracting structured data from a single comment posted in a Hacker News "Who is hiring?" thread. Each such comment is one job posting from a hiring company.

Extract:
- location: the work location/policy as stated (e.g. "Remote", "Remote (US)", "On-site San Francisco", "Hybrid from the NL")
- job_title: the role being hired for
- description: the job description (company + role details), as written
- how_to_apply: the URL, email address, or instructions given to apply
- has_salary: true if a salary or compensation range is stated anywhere in the comment, false otherwise
- salary_actual: the salary exactly as written (e.g. "100-200k CHF"), or "" if has_salary is false
- salary_min_amount: the minimum salary as a plain number with any "k"/"m" suffix expanded (e.g. "100k" -> 100000), or 0 if has_salary is false. If only one figure is given, use it for both min and max.
- salary_max_amount: the maximum salary as a plain number, same rules as salary_min_amount
- salary_currency_code: the 3-letter ISO 4217 currency code (e.g. "USD", "EUR", "CHF") for the stated salary, or "" if has_salary is false

Return only the structured fields below. Do not invent information that is not present in the comment.`

// GeminiExtractor implements JobExtractor using the Gemini API's structured
// JSON output mode.
type GeminiExtractor struct {
	client *genai.Client
	model  string
}

func newGeminiExtractor(apiKey string) (*GeminiExtractor, error) {
	return newGeminiExtractorWithOptions(apiKey, "", defaultGeminiModel)
}

func newGeminiExtractorWithOptions(apiKey, baseURL, model string) (*GeminiExtractor, error) {
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: baseURL,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}
	return &GeminiExtractor{client: client, model: model}, nil
}

var jobPostingSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"location":             {Type: genai.TypeString},
		"job_title":            {Type: genai.TypeString},
		"description":          {Type: genai.TypeString},
		"how_to_apply":         {Type: genai.TypeString},
		"has_salary":           {Type: genai.TypeBoolean},
		"salary_actual":        {Type: genai.TypeString},
		"salary_min_amount":    {Type: genai.TypeNumber},
		"salary_max_amount":    {Type: genai.TypeNumber},
		"salary_currency_code": {Type: genai.TypeString},
	},
	Required: []string{"location", "job_title", "description", "how_to_apply", "has_salary"},
}

type geminiJobResponse struct {
	Location           string  `json:"location"`
	JobTitle           string  `json:"job_title"`
	Description        string  `json:"description"`
	HowToApply         string  `json:"how_to_apply"`
	HasSalary          bool    `json:"has_salary"`
	SalaryActual       string  `json:"salary_actual"`
	SalaryMinAmount    float64 `json:"salary_min_amount"`
	SalaryMaxAmount    float64 `json:"salary_max_amount"`
	SalaryCurrencyCode string  `json:"salary_currency_code"`
}

// ExtractJob asks Gemini to extract structured job fields from commentText.
// SalaryNormalizedUSD is deliberately left zero here — it is computed
// downstream by internal/cli using internal/fx, not by the LLM.
func (e *GeminiExtractor) ExtractJob(ctx context.Context, commentText string) (job.JobPosting, error) {
	contents := genai.Text(extractionPrompt + "\n\nComment:\n" + commentText)

	resp, err := e.client.Models.GenerateContent(ctx, e.model, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   jobPostingSchema,
	})
	if err != nil {
		return job.JobPosting{}, fmt.Errorf("gemini generate content: %w", err)
	}

	var parsed geminiJobResponse
	if err := json.Unmarshal([]byte(resp.Text()), &parsed); err != nil {
		return job.JobPosting{}, fmt.Errorf("parsing gemini response: %w", err)
	}

	return job.JobPosting{
		Location:           parsed.Location,
		JobTitle:           parsed.JobTitle,
		Description:        parsed.Description,
		HowToApply:         parsed.HowToApply,
		HasSalary:          parsed.HasSalary,
		SalaryActual:       parsed.SalaryActual,
		SalaryMinAmount:    parsed.SalaryMinAmount,
		SalaryMaxAmount:    parsed.SalaryMaxAmount,
		SalaryCurrencyCode: parsed.SalaryCurrencyCode,
	}, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/extract/... -v`
Expected: PASS (four tests: two `TestResolveExtractor_*`, plus `TestGeminiExtractor_ExtractJob`).

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/extract
git commit -m "Add LLM extraction interface and Gemini structured-output extractor"
```

---

## Task 5: `internal/cli` orchestration + `cmd/hnwih` entrypoint

**Files:**
- Create: `internal/cli/run.go`
- Test: `internal/cli/run_test.go`
- Create: `cmd/hnwih/main.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `hn.Client`, `hn.Comment`, `hn.ParseThreadID`, `hn.NewAlgoliaClient` (Task 2); `extract.JobExtractor`, `extract.Config`, `extract.ResolveExtractor` (Task 4); `fx.CurrencyConverter`, `fx.NewFrankfurterConverter`, `(*fx.FrankfurterConverter).FetchRates` (Task 3); `job.JobPosting`, `job.WriteCSV` (Task 1).
- Produces:
  - `type Config struct { ThreadURL, OutPath string; Concurrency int }`
  - `type Deps struct { HN hn.Client; Extractor extract.JobExtractor; Converter fx.CurrencyConverter }`
  - `type Summary struct { Written, Skipped int }`
  - `func Run(ctx context.Context, cfg Config, deps Deps, stderr io.Writer) (Summary, error)`
  - the `hnwih` binary (`cmd/hnwih`)

- [ ] **Step 1: Write the failing orchestration tests**

Create `internal/cli/run_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -v`
Expected: FAIL — compile error, `Run`, `Config`, `Deps`, `Summary` are undefined.

- [ ] **Step 3: Implement `internal/cli/run.go`**

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Add the cobra dependency**

Run:
```bash
go get github.com/spf13/cobra@latest
```

- [ ] **Step 6: Implement `cmd/hnwih/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/cli"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/extract"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/fx"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/hn"
)

func main() {
	var urlFlag, outFlag, geminiAPIKeyFlag string
	var concurrency int

	rootCmd := &cobra.Command{
		Use:   "hnwih",
		Short: `Scrape a Hacker News "Who is hiring?" thread into structured CSV`,
		RunE: func(cmd *cobra.Command, args []string) error {
			geminiAPIKey := geminiAPIKeyFlag
			if geminiAPIKey == "" {
				geminiAPIKey = os.Getenv("GEMINI_API_KEY")
			}

			extractor, err := extract.ResolveExtractor(extract.Config{GeminiAPIKey: geminiAPIKey})
			if err != nil {
				return err
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			hnClient := hn.NewAlgoliaClient(httpClient)
			converter := fx.NewFrankfurterConverter(httpClient)

			ctx := context.Background()
			if err := converter.FetchRates(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not fetch FX rates, salary normalization disabled: %v\n", err)
			}

			_, err = cli.Run(ctx, cli.Config{
				ThreadURL:   urlFlag,
				OutPath:     outFlag,
				Concurrency: concurrency,
			}, cli.Deps{
				HN:        hnClient,
				Extractor: extractor,
				Converter: converter,
			}, os.Stderr)
			return err
		},
	}

	rootCmd.Flags().StringVar(&urlFlag, "url", "", "HN thread URL or numeric ID (required)")
	rootCmd.Flags().StringVar(&outFlag, "out", "jobs.csv", "output CSV path")
	rootCmd.Flags().StringVar(&geminiAPIKeyFlag, "gemini-api-key", "", "Gemini API key (or set GEMINI_API_KEY)")
	rootCmd.Flags().IntVar(&concurrency, "concurrency", 5, "number of comments processed concurrently")
	_ = rootCmd.MarkFlagRequired("url")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 7: Build the binary and verify flag validation without any network calls**

Run:
```bash
go build -o /tmp/hnwih ./cmd/hnwih
/tmp/hnwih
```
Expected: build succeeds; running with no flags fails fast with a cobra "required flag(s) \"url\" not set" error (no network activity, since cobra validates required flags before `RunE` runs).

Run:
```bash
/tmp/hnwih --url 49522897
```
Expected: fails fast with `no LLM provider configured: set --gemini-api-key or GEMINI_API_KEY` (still no network activity — `ResolveExtractor` is checked before any HN/FX/Gemini call).

- [ ] **Step 8: Update `README.md`**

Replace the entire contents of `README.md` with:

```markdown
# hacker-news-who-ishiring-scrapper

A CLI that scrapes an "Ask HN: Who is hiring?" thread's top-level comments
into a structured, salary-normalized CSV file, using Gemini for extraction.

## Usage

```bash
export GEMINI_API_KEY=your-gemini-api-key
go run ./cmd/hnwih --url https://news.ycombinator.com/item?id=49522897 --out jobs.csv
```

Or pass the numeric thread ID directly: `--url 49522897`.

## Flags

| Flag | Env var | Required | Default |
|---|---|---|---|
| `--url` | — | yes | — |
| `--out` | — | no | `jobs.csv` |
| `--gemini-api-key` | `GEMINI_API_KEY` | yes (selects the Gemini provider) | — |
| `--concurrency` | — | no | `5` |

## Output

A CSV with one row per top-level comment (job posting) in the thread:

```
location, job_title, description, how_to_apply, salary_actual,
salary_min_amount, salary_max_amount, salary_currency_code, salary_normalized_usd
```

See [docs/superpowers/specs/2026-09-17-hn-job-scraper-design.md](docs/superpowers/specs/2026-09-17-hn-job-scraper-design.md)
for the full design.
```

- [ ] **Step 9: Run the full test suite and build one more time**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: build succeeds, `go vet` reports nothing, all tests across all packages PASS.

- [ ] **Step 10: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/cli cmd/hnwih README.md
git commit -m "Wire CLI orchestration and cobra entrypoint"
```

---

## Self-Review Notes

- **Spec coverage:** Data source/filtering (Task 2), schema + CSV (Task 1), FX caching guarantee (Task 3, explicitly tested), provider-inferred-from-API-key config (Task 4), structured Gemini output (Task 4), error handling and concurrency (Task 5), configuration table and dependencies (Task 5 README + go.mod) — every spec section maps to a task.
- **Placeholder scan:** no TBD/TODO; every step has real, runnable code or an exact command with an exact expected result.
- **Type consistency:** `job.JobPosting` fields are identical across Tasks 1, 4, and 5; `hn.Comment{ID, Text}` is identical across Tasks 2 and 5; `extract.JobExtractor`/`fx.CurrencyConverter`/`hn.Client` interface method signatures match between their defining package and every consumer (Task 5's `Deps` and fakes).
