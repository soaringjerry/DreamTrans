package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveRateRefreshAppliesMarkupAndKeepsLastQuote(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("base") != "USD" || r.URL.Query().Get("symbols") != "AUD" {
			t.Errorf("unexpected query %q", r.URL.RawQuery)
		}
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"amount":1.0,"base":"USD","date":"2026-09-03","rates":{"AUD":1.5}}`))
	}))
	defer server.Close()

	live := newLiveRate("aud", server.URL, 2)
	if _, ok := live.Current(); ok {
		t.Fatal("rate must be unavailable before the first fetch")
	}
	if err := live.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	rate, ok := live.Current()
	if !ok || rate != 1.53 {
		t.Fatalf("Current() = %v,%v want 1.53 (1.5 + 2%%)", rate, ok)
	}
	if live.Describe() != "1.5000 (2026-09-03, +2%)" {
		t.Fatalf("Describe() = %q", live.Describe())
	}

	// A failed refresh keeps the previous quote.
	fail.Store(true)
	if err := live.Refresh(context.Background()); err == nil {
		t.Fatal("expected refresh error")
	}
	if rate, ok := live.Current(); !ok || rate != 1.53 {
		t.Fatalf("after failure Current() = %v,%v want 1.53", rate, ok)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}

	// ...but not forever: a week-old quote is no longer trusted.
	live.now = func() time.Time { return time.Now().Add(fxMaxAge + time.Minute) }
	if _, ok := live.Current(); ok {
		t.Fatal("stale quote must be rejected")
	}
}

func TestLiveRateRejectsUnusableQuotes(t *testing.T) {
	for _, body := range []string{`{}`, `{"rates":{"AUD":0}}`, `{"rates":{"EUR":0.9}}`, `not json`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		live := newLiveRate("aud", server.URL, 0)
		if err := live.Refresh(context.Background()); err == nil {
			t.Errorf("body %q: expected error", body)
		}
		server.Close()
	}
}

func TestAutoRateClientRefusesCheckoutWithoutQuote(t *testing.T) {
	c := &StripeClient{secretKey: "sk_test", currency: "aud", live: newLiveRate("aud", "http://127.0.0.1:1", 0)}
	if c.Ready() || c.USDRate() != 0 {
		t.Fatalf("client without a quote must not be ready: ready=%v rate=%v", c.Ready(), c.USDRate())
	}
	if _, err := c.CreateTopupCheckout(context.Background(), &CheckoutInput{AmountUSD: 20}); !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("CreateTopupCheckout error = %v, want ErrRateUnavailable", err)
	}
	if _, err := c.CreateMembershipCheckout(context.Background(), &CheckoutInput{PriceUSD: 6}); !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("CreateMembershipCheckout error = %v, want ErrRateUnavailable", err)
	}
	if _, err := c.ChargeOffSession(context.Background(), "cus_1", 20, "auto", "key"); !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("ChargeOffSession error = %v, want ErrRateUnavailable", err)
	}
	// Legacy webhook amounts still convert (at par) rather than dividing by zero.
	if got := c.USDFromMinor(2000, "aud", 0); got != 20 {
		t.Fatalf("USDFromMinor without any rate = %v, want 20", got)
	}
}
