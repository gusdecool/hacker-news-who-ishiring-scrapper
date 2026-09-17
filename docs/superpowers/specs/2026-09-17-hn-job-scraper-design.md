# HN "Who is Hiring" Scraper — Design

Source issue: https://github.com/gusdecool/hacker-news-who-ishiring-scrapper/issues/1

## Goal

A Go CLI that fetches a monthly "Ask HN: Who is hiring?" thread, extracts
one structured job record per top-level comment using an LLM (Gemini for
v1, provider-agnostic interface for later), converts stated salaries to a
normalized USD figure, and writes the result to a CSV file for filtering
(e.g. "only remote-global roles").

## Non-goals (v1)

- No filtering/query subcommand — the CSV itself is the filtering
  interface for now (open in a spreadsheet, sort/filter there).
- No processing of nested replies under a job comment — only direct
  top-level comments of the thread are job postings; replies are
  discussion, not listings.
- No persistent cross-run caching of FX rates — the thread is scraped at
  most a few times a month, so an in-memory per-run cache is sufficient.
- No database/storage layer — output is a single CSV file per run.

## Data source

The example URL in the issue (`item?id=49522897`) is the thread's root
**story** post. Its own `text` is only the posting guidelines
("Please state the location and include REMOTE...") — it contains no job
data. The job postings are its direct child comments (263 of them for the
September 2026 thread, confirmed via the HN Algolia API). Nested replies
under each job comment are follow-up discussion, not job postings, and are
ignored.

The scraper uses the HN Algolia API
(`https://hn.algolia.com/api/v1/items/{id}`), which returns the story and
its comment tree in a single JSON response — no need to walk
`kids`/recursive Firebase API calls. From that response the scraper takes
only the direct children of the root item, skipping any marked
dead/deleted.

## Architecture

Single Go module, one binary, one CLI command for v1.

```
cmd/hnwih/            main.go — CLI entrypoint (cobra)
internal/hn/          HN client: fetch thread + top-level comments
internal/extract/     JobExtractor interface + Gemini implementation
internal/fx/          CurrencyConverter interface + live-rate implementation
internal/job/         JobPosting struct (shared schema) + CSV writer
```

### `internal/hn`

```go
type Comment struct {
    ID   int
    Text string // raw comment body (HTML-escaped HN markup)
}

type Client interface {
    FetchThread(ctx context.Context, threadID string) ([]Comment, error)
}
```

`FetchThread` makes one HTTP GET to the Algolia API, parses the response,
and returns the direct children of the root item as `Comment`s, filtering
out dead/deleted/flagged entries. `threadID` accepts either a raw numeric
ID or a full `news.ycombinator.com/item?id=...` URL (ID extracted from the
query string).

### `internal/extract`

```go
type JobExtractor interface {
    ExtractJob(ctx context.Context, commentText string) (job.JobPosting, error)
}
```

`GeminiExtractor` implements this using `google.golang.org/genai`,
requesting structured output via a JSON response schema matching
`JobPosting`'s extractable fields (see Schema below) so no manual
JSON-parsing/retry logic is needed.

Provider selection is config-driven through a small registry:

```go
var extractors = map[string]func(cfg Config) (JobExtractor, error){
    "gemini": newGeminiExtractor,
}
```

`--llm-provider` (default `gemini`) selects the entry. Adding a second
provider later means one new file implementing `JobExtractor` plus one map
entry — no interface changes.

### `internal/fx`

```go
type CurrencyConverter interface {
    ToUSD(ctx context.Context, amount float64, currencyCode string) (float64, error)
}
```

The live implementation calls the Frankfurter API (free, no API key)
**once per program run** to fetch the full latest rate table, caches it in
an in-memory map, and every `ToUSD` call thereafter is a local lookup — no
additional HTTP requests regardless of how many jobs or distinct
currencies appear in the thread. If the initial rate fetch fails, the
converter returns an error for every `ToUSD` call for the rest of the run
(callers treat this as "salary normalization unavailable," not fatal to
the whole run — see Error Handling).

### `internal/job`

```go
type JobPosting struct {
    Location            string
    JobTitle            string
    Description         string
    HowToApply          string
    SalaryActual        string  // original text as written, e.g. "100-200k CHF"
    SalaryMinAmount     float64 // 0 if not stated
    SalaryMaxAmount     float64 // 0 if not stated / equals min if a single figure was given
    SalaryCurrencyCode  string  // ISO 4217, e.g. "CHF"; empty if no salary stated
    SalaryNormalizedUSD float64 // computed in Go, not by the LLM; 0 + HasSalary=false if not stated
    HasSalary           bool    // distinguishes "no salary stated" from "$0"
}

func WriteCSV(path string, jobs []JobPosting) error
```

CSV columns (header row), in order:

```
location, job_title, description, how_to_apply, salary_actual,
salary_min_amount, salary_max_amount, salary_currency_code,
salary_normalized_usd
```

`salary_min_amount`, `salary_max_amount`, `salary_currency_code`, and
`salary_normalized_usd` are written as empty strings (not `0`) when
`HasSalary` is false, to distinguish "no data" from "$0".

## Data flow

1. CLI parses flags/env, resolves config (thread ID/URL, output path,
   LLM provider + API key).
2. `hn.Client.FetchThread` — one HTTP call, returns top-level `Comment`s.
3. `fx.Converter` fetches the full rate table once, up front, before any
   comment is processed (it's a single HTTP call, so there's no benefit
   to deferring it).
4. A bounded worker pool (default concurrency 5, configurable via
   `--concurrency`) calls `extract.ExtractJob` for each comment.
5. For each successfully extracted job with `HasSalary == true`, compute
   `SalaryNormalizedUSD = fx.ToUSD((SalaryMinAmount+SalaryMaxAmount)/2, SalaryCurrencyCode)`.
6. Collect all successfully extracted `JobPosting`s (skipping failed
   ones — see Error Handling) and call `job.WriteCSV`.
7. Print a summary line: `N jobs written, M skipped (errors)` to stderr.

## Error handling

- **HN fetch failure** (network error, thread not found): fatal, exit
  non-zero immediately with a clear message. There's nothing to process.
- **Per-comment extraction failure** (Gemini API error, malformed
  response after SDK-level retries): log a warning with the comment's HN
  item ID, skip that record, continue with the rest. The command exits
  non-zero only if **zero** jobs were successfully extracted overall.
- **FX rate fetch failure**: not fatal. Every job's
  `SalaryNormalizedUSD`/`HasSalary` conversion step logs a warning once
  and the CSV row keeps `salary_actual` but leaves the normalized/derived
  salary columns empty. Non-salary fields (location, title, description,
  etc.) are unaffected.
- **Config errors** (missing required API key, invalid thread
  URL/ID): fatal at startup, before any network calls, with a message
  naming the missing/invalid value.

## Configuration

Resolution order: CLI flag > environment variable > error if a required
value is missing.

| Flag | Env var | Required | Default |
|---|---|---|---|
| `--url` | — | yes | — |
| `--out` | — | no | `jobs.csv` |
| `--llm-provider` | — | no | `gemini` |
| `--gemini-api-key` | `GEMINI_API_KEY` | yes (if provider=gemini) | — |
| `--concurrency` | — | no | `5` |

## Testing

- **`internal/hn`**: table-driven tests against an `httptest` server
  returning fixture Algolia JSON, including dead/deleted comments, to
  verify filtering and URL/ID parsing.
- **`internal/extract`**: `JobExtractor` is an interface — everything
  that consumes it (the CLI's orchestration logic) is tested against a
  fake implementation. `GeminiExtractor` itself gets a thin test verifying
  the request/schema shape against a mocked HTTP transport; no live API
  calls in tests.
- **`internal/fx`**: same pattern — interface + fake for consumers; the
  live implementation is tested against a mocked HTTP response, including
  a test asserting `ToUSD` makes no further HTTP calls after the initial
  rate fetch (the caching guarantee this spec is built around).
- **`internal/job`**: CSV writer tested directly against `[]JobPosting`
  fixtures, checking header, row formatting, and the empty-vs-zero
  handling for `HasSalary == false` rows.
- **CLI**: an integration test wiring fake `hn.Client` + fake
  `JobExtractor` + fake `CurrencyConverter` together to verify the
  end-to-end flow, the skip-and-continue behavior on partial failures, and
  the summary line/exit code.

## Dependencies

- `google.golang.org/genai` — Gemini Go SDK, structured output.
- `github.com/spf13/cobra` — CLI framework.
- Frankfurter API (`https://api.frankfurter.dev`) — free FX rates, no
  client library needed (plain HTTP + JSON).
