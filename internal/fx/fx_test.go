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
