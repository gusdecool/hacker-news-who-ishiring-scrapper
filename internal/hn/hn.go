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
