package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// TestGeminiExtractor_RetriesOnTransientFailure proves the client is
// configured to retry: the server returns 429 on the first request and a
// valid response on the second, and ExtractJob must still succeed.
func TestGeminiExtractor_RetriesOnTransientFailure(t *testing.T) {
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requestCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"code": 429, "message": "rate limited"}}`))
			return
		}

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
	if got.JobTitle != "Senior Product Engineer" {
		t.Errorf("unexpected extraction result: %+v", got)
	}

	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Errorf("expected exactly 2 requests (1 failure + 1 retry), got %d", got)
	}
}
