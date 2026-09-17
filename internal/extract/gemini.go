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
