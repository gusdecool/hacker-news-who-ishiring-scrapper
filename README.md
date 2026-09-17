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
