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
