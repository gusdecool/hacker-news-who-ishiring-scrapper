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

func newFakeGeminiServer(t *testing.T, handler http.HandlerFunc) *GeminiExtractor {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	extractor, err := newGeminiExtractorWithOptions("test-key", server.URL, "test-model")
	if err != nil {
		t.Fatalf("newGeminiExtractorWithOptions returned error: %v", err)
	}
	return extractor
}

func jsonCandidateResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"text": text},
					},
				},
			},
		},
	})
}

func TestGeminiExtractor_ExtractJobs(t *testing.T) {
	var capturedBody string

	extractor := newFakeGeminiServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)

		responseJSON := `[
			{
				"comment_id": 1,
				"location": "Remote (Europe)",
				"job_title": "Senior Product Engineer",
				"description": "Modash helps brands find creators.",
				"how_to_apply": "https://modash.io",
				"has_salary": true,
				"salary_actual": "€75k-110k",
				"salary_min_amount": 75000,
				"salary_max_amount": 110000,
				"salary_currency_code": "EUR"
			},
			{
				"comment_id": 2,
				"location": "On-site NYC",
				"job_title": "Backend Engineer",
				"description": "Acme is hiring.",
				"how_to_apply": "jobs@acme.com",
				"has_salary": false,
				"salary_actual": "",
				"salary_min_amount": 0,
				"salary_max_amount": 0,
				"salary_currency_code": ""
			}
		]`
		jsonCandidateResponse(w, responseJSON)
	})

	got, err := extractor.ExtractJobs(context.Background(), []CommentInput{
		{ID: 1, Text: "Modash.io | Senior Product Engineer | Remote (Europe)"},
		{ID: 2, Text: "Acme | Backend Engineer | On-site NYC"},
	})
	if err != nil {
		t.Fatalf("ExtractJobs returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}

	if got[0].CommentID != 1 || got[0].Err != nil || got[0].Posting.JobTitle != "Senior Product Engineer" {
		t.Errorf("unexpected result[0]: %+v", got[0])
	}
	if !got[0].Posting.HasSalary || got[0].Posting.SalaryMinAmount != 75000 || got[0].Posting.SalaryMaxAmount != 110000 || got[0].Posting.SalaryCurrencyCode != "EUR" {
		t.Errorf("unexpected salary fields: %+v", got[0].Posting)
	}

	if got[1].CommentID != 2 || got[1].Err != nil || got[1].Posting.JobTitle != "Backend Engineer" {
		t.Errorf("unexpected result[1]: %+v", got[1])
	}

	if !strings.Contains(capturedBody, `"responseMimeType":"application/json"`) {
		t.Errorf("request did not request structured JSON output, body: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, `"responseSchema"`) {
		t.Errorf("request did not include a response schema, body: %s", capturedBody)
	}
}

// TestGeminiExtractor_MissingCommentGetsPerItemError proves that a comment
// absent from the model's response fails only that comment, not the batch.
func TestGeminiExtractor_MissingCommentGetsPerItemError(t *testing.T) {
	extractor := newFakeGeminiServer(t, func(w http.ResponseWriter, r *http.Request) {
		responseJSON := `[
			{
				"comment_id": 1,
				"location": "Remote",
				"job_title": "Engineer",
				"description": "desc",
				"how_to_apply": "apply@x.com",
				"has_salary": false,
				"salary_actual": "",
				"salary_min_amount": 0,
				"salary_max_amount": 0,
				"salary_currency_code": ""
			}
		]`
		jsonCandidateResponse(w, responseJSON)
	})

	got, err := extractor.ExtractJobs(context.Background(), []CommentInput{
		{ID: 1, Text: "Company A"},
		{ID: 2, Text: "Company B"},
	})
	if err != nil {
		t.Fatalf("ExtractJobs returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}

	if got[0].Err != nil {
		t.Errorf("expected comment 1 to succeed, got err: %v", got[0].Err)
	}
	if got[1].Err == nil {
		t.Error("expected comment 2 (missing from response) to have a per-item error")
	}
}

// TestGeminiExtractor_RetriesOnTransientFailure proves the client is
// configured to retry: the server returns 429 on the first request and a
// valid response on the second, and ExtractJobs must still succeed.
func TestGeminiExtractor_RetriesOnTransientFailure(t *testing.T) {
	var requestCount int32

	extractor := newFakeGeminiServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requestCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"code": 429, "message": "rate limited"}}`))
			return
		}

		responseJSON := `[
			{
				"comment_id": 1,
				"location": "Remote (Europe)",
				"job_title": "Senior Product Engineer",
				"description": "Modash helps brands find creators.",
				"how_to_apply": "https://modash.io",
				"has_salary": true,
				"salary_actual": "€75k-110k",
				"salary_min_amount": 75000,
				"salary_max_amount": 110000,
				"salary_currency_code": "EUR"
			}
		]`
		jsonCandidateResponse(w, responseJSON)
	})

	got, err := extractor.ExtractJobs(context.Background(), []CommentInput{
		{ID: 1, Text: "Modash.io | Senior Product Engineer | Remote (Europe)"},
	})
	if err != nil {
		t.Fatalf("ExtractJobs returned error: %v", err)
	}
	if len(got) != 1 || got[0].Posting.JobTitle != "Senior Product Engineer" {
		t.Errorf("unexpected extraction result: %+v", got)
	}

	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Errorf("expected exactly 2 requests (1 failure + 1 retry), got %d", got)
	}
}
