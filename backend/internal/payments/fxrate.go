package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrRateUnavailable is returned when checkout needs a live USD exchange rate
// and none has been fetched yet (or the cached one is too old to trust).
var ErrRateUnavailable = errors.New("usd exchange rate unavailable")

const (
	// defaultFXRateURL serves ECB reference rates (Frankfurter, no API key).
	defaultFXRateURL  = "https://api.frankfurter.dev/v1/latest"
	fxRefreshInterval = time.Hour
	fxRetryInterval   = 5 * time.Minute
	// fxMaxAge bounds how long a fetched rate is used when refreshes keep
	// failing. ECB rates are daily on business days, so a week of silence
	// means something is wrong.
	fxMaxAge      = 7 * 24 * time.Hour
	fxFetchLimit  = 64 * 1024
	fxFetchTimout = 10 * time.Second
)

// liveRate fetches how many units of currency one US dollar buys and caches
// the answer. The rate handed to checkout includes a configurable markup so
// day-to-day drift and Stripe's conversion spread do not eat the margin.
type liveRate struct {
	currency string  // lower-case ISO 4217
	endpoint string  // latest-rates endpoint
	markup   float64 // percent added on top of the reference rate
	client   *http.Client
	now      func() time.Time

	mu      sync.RWMutex
	rate    float64 // reference rate, no markup
	asOf    string  // provider's quote date
	fetched time.Time
}

func newLiveRate(currency, endpoint string, markup float64) *liveRate {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultFXRateURL
	}
	return &liveRate{
		currency: strings.ToLower(currency),
		endpoint: endpoint,
		markup:   markup,
		client:   &http.Client{Timeout: fxFetchTimout},
		now:      time.Now,
	}
}

// Current returns the marked-up USD rate, or false when no trustworthy rate
// is cached.
func (l *liveRate) Current() (float64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !(l.rate > 0) || l.now().Sub(l.fetched) > fxMaxAge {
		return 0, false
	}
	return math.Round(l.rate*(1+l.markup/100)*10000) / 10000, true
}

// Describe reports the cached quote for logs, e.g. "1.5312 (ECB 2026-09-03, +2%)".
func (l *liveRate) Describe() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !(l.rate > 0) {
		return "unavailable"
	}
	return fmt.Sprintf("%.4f (%s, +%g%%)", l.rate, l.asOf, l.markup)
}

// Refresh fetches the latest quote. A failure leaves the previous quote in
// place so a transient outage does not take checkout down.
func (l *liveRate) Refresh(ctx context.Context) error {
	endpoint, err := url.Parse(l.endpoint)
	if err != nil {
		return fmt.Errorf("fx rate url: %w", err)
	}
	query := endpoint.Query()
	query.Set("base", "USD")
	query.Set("symbols", strings.ToUpper(l.currency))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch fx rate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch fx rate: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, fxFetchLimit)).Decode(&payload); err != nil {
		return fmt.Errorf("decode fx rate: %w", err)
	}
	rate, ok := payload.Rates[strings.ToUpper(l.currency)]
	if !ok || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return fmt.Errorf("fx rate response has no usable USD/%s quote", strings.ToUpper(l.currency))
	}
	l.mu.Lock()
	l.rate, l.asOf, l.fetched = rate, payload.Date, l.now()
	l.mu.Unlock()
	return nil
}

// Run refreshes hourly until ctx ends, retrying sooner after a failure.
func (l *liveRate) Run(ctx context.Context) {
	for {
		wait := fxRefreshInterval
		if err := l.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("stripe fx rate refresh failed (keeping %s): %v", l.Describe(), err)
			wait = fxRetryInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
