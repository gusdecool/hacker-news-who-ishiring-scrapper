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
