package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/job"
)

const defaultGeminiModel = "gemini-flash-latest"

// requestTimeout bounds a single HTTP attempt to Gemini. Left unset, the SDK
// defaults to &http.Client{} (zero Timeout), so a single stalled connection
// blocks that comment's extraction — and its concurrency slot — forever.
const requestTimeout = 60 * time.Second

const extractionPrompt = `You are extracting structured data from a batch of comments posted in a Hacker News "Who is hiring?" thread. Each comment is one job posting from a hiring company, and is given below prefixed with its comment_id.

For every comment in the batch, extract:
- comment_id: the comment's id, copied exactly from its "comment_id:" prefix
- location: the work location/policy as stated (e.g. "Remote", "Remote (US)", "On-site San Francisco", "Hybrid from the NL")
- job_title: the role being hired for
- description: the job description (company + role details), as written
- how_to_apply: the URL, email address, or instructions given to apply
- has_salary: true if a salary or compensation range is stated anywhere in the comment, false otherwise
- salary_actual: the salary exactly as written (e.g. "100-200k CHF"), or "" if has_salary is false
- salary_min_amount: the minimum salary as a plain number with any "k"/"m" suffix expanded (e.g. "100k" -> 100000), or 0 if has_salary is false. If only one figure is given, use it for both min and max.
- salary_max_amount: the maximum salary as a plain number, same rules as salary_min_amount
- salary_currency_code: the 3-letter ISO 4217 currency code (e.g. "USD", "EUR", "CHF") for the stated salary, or "" if has_salary is false

Return a JSON array with exactly one object per input comment, each including its comment_id. Do not invent information that is not present in a comment. Do not merge, skip, or add comments.`

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
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: &http.Client{Timeout: requestTimeout},
		HTTPOptions: genai.HTTPOptions{
			BaseURL: baseURL,
			// A nil RetryOptions means zero retries in this SDK. An empty
			// (non-nil) HTTPRetryOptions activates its documented defaults:
			// 5 attempts, exponential backoff, retrying on 408/429/5xx and
			// transport errors. Without this, a transient rate limit or
			// server error silently drops that comment's extraction.
			RetryOptions: &genai.HTTPRetryOptions{},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}
	return &GeminiExtractor{client: client, model: model}, nil
}

var jobBatchSchema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"comment_id":           {Type: genai.TypeInteger},
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
		Required: []string{"comment_id", "location", "job_title", "description", "how_to_apply", "has_salary"},
	},
}

type geminiJobResponse struct {
	CommentID          int     `json:"comment_id"`
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

// buildBatchContent renders the prompt followed by every comment in the
// batch, each prefixed with the comment_id the model must echo back so
// results can be matched to comments regardless of response order.
func buildBatchContent(comments []CommentInput) string {
	var b strings.Builder
	b.WriteString(extractionPrompt)
	for _, c := range comments {
		fmt.Fprintf(&b, "\n\n---\ncomment_id: %d\n%s", c.ID, c.Text)
	}
	return b.String()
}

// ExtractJobs asks Gemini to extract structured job fields for every
// comment in the batch in a single request. SalaryNormalizedUSD is
// deliberately left zero here — it is computed downstream by internal/cli
// using internal/fx, not by the LLM.
//
// A non-nil error means the whole batch failed. On success, a comment
// missing from the model's response gets its own JobResult.Err rather than
// failing the other comments in the batch.
func (e *GeminiExtractor) ExtractJobs(ctx context.Context, comments []CommentInput) ([]JobResult, error) {
	if len(comments) == 0 {
		return nil, nil
	}

	contents := genai.Text(buildBatchContent(comments))

	resp, err := e.client.Models.GenerateContent(ctx, e.model, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   jobBatchSchema,
		// This is a straightforward field-extraction task, not one needing
		// reasoning. Without this, gemini-flash-latest (currently aliasing to
		// a thinking model) spends hundreds of hidden "thinking" tokens per
		// comment, multiplying latency and cost across a thread's comments
		// for no gain in extraction quality.
		ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: genai.Ptr(int32(0))},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini generate content: %w", err)
	}

	var parsed []geminiJobResponse
	if err := json.Unmarshal([]byte(resp.Text()), &parsed); err != nil {
		return nil, fmt.Errorf("parsing gemini response: %w", err)
	}

	byCommentID := make(map[int]geminiJobResponse, len(parsed))
	for _, p := range parsed {
		byCommentID[p.CommentID] = p
	}

	results := make([]JobResult, len(comments))
	for i, c := range comments {
		p, ok := byCommentID[c.ID]
		if !ok {
			results[i] = JobResult{CommentID: c.ID, Err: fmt.Errorf("comment %d: missing from batch response", c.ID)}
			continue
		}
		results[i] = JobResult{
			CommentID: c.ID,
			Posting: job.JobPosting{
				Location:           p.Location,
				JobTitle:           p.JobTitle,
				Description:        p.Description,
				HowToApply:         p.HowToApply,
				HasSalary:          p.HasSalary,
				SalaryActual:       p.SalaryActual,
				SalaryMinAmount:    p.SalaryMinAmount,
				SalaryMaxAmount:    p.SalaryMaxAmount,
				SalaryCurrencyCode: p.SalaryCurrencyCode,
			},
		}
	}
	return results, nil
}
