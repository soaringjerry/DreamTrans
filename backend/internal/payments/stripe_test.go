package payments

import (
	"strings"
	"testing"

	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/form"
)

// Newer Stripe accounts default to Managed Payments, which rejects
// setup_future_usage. Every Checkout Session must opt out explicitly.
func TestDisableManagedPaymentsEncodesRawField(t *testing.T) {
	params := &stripe.CheckoutSessionParams{Mode: stripe.String("payment")}
	disableManagedPayments(&params.Params)
	values := &form.Values{}
	form.AppendTo(values, params)
	encoded := values.Encode()
	if !strings.Contains(encoded, "managed_payments[enabled]=false") {
		t.Fatalf("managed_payments[enabled]=false missing from %q", encoded)
	}
}

// Saving the payment method for automatic top-ups must be scoped to cards.
// A top-level payment_intent_data[setup_future_usage] makes Checkout hide
// wallets such as WeChat Pay and Alipay that cannot be reused off-session.
func TestTopupCheckoutScopesSetupFutureUsageToCards(t *testing.T) {
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String("payment"),
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{},
	}
	saveCardForOffSession(params)
	values := &form.Values{}
	form.AppendTo(values, params)
	encoded := values.Encode()
	if !strings.Contains(encoded, "payment_method_options[card][setup_future_usage]=off_session") {
		t.Fatalf("card-scoped setup_future_usage missing from %q", encoded)
	}
	if strings.Contains(encoded, "payment_intent_data[setup_future_usage]") {
		t.Fatalf("top-level setup_future_usage must not be set: %q", encoded)
	}
}

func TestParseCheckoutSessionPaidStatus(t *testing.T) {
	cases := map[string]bool{
		`{"id":"cs_1","mode":"payment","payment_status":"paid"}`:                true,
		`{"id":"cs_2","mode":"payment","payment_status":"no_payment_required"}`: true,
		`{"id":"cs_3","mode":"payment"}`:                                        true,
		`{"id":"cs_4","mode":"payment","payment_status":"unpaid"}`:              false,
	}
	for raw, want := range cases {
		session, err := ParseCheckoutSession([]byte(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got := session.Paid(); got != want {
			t.Errorf("Paid() for %s = %v, want %v", raw, got, want)
		}
	}
}

func TestParseCheckoutCurrency(t *testing.T) {
	cases := []struct {
		currency, rate string
		wantCurrency   string
		wantRate       float64
		wantErr        bool
	}{
		{"", "", "usd", 1, false},
		{"USD", "1", "usd", 1, false},
		{"aud", "1.55", "aud", 1.55, false},
		{" AUD ", "1.5", "aud", 1.5, false},
		{"aud", "", "", 0, true},    // rate required for non-USD
		{"usd", "1.5", "", 0, true}, // USD is the ledger currency
		{"aud", "0", "", 0, true},
		{"aud", "-1", "", 0, true},
		{"aud", "abc", "", 0, true},
		{"jpy", "150", "", 0, true}, // zero-decimal
		{"au", "1.5", "", 0, true},
		{"a1d", "1.5", "", 0, true},
	}
	for _, tc := range cases {
		currency, rate, err := parseCheckoutCurrency(tc.currency, tc.rate)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseCheckoutCurrency(%q,%q) expected error", tc.currency, tc.rate)
			}
			continue
		}
		if err != nil || currency != tc.wantCurrency || rate != tc.wantRate {
			t.Errorf("parseCheckoutCurrency(%q,%q) = %q,%v,%v want %q,%v", tc.currency, tc.rate, currency, rate, err, tc.wantCurrency, tc.wantRate)
		}
	}
}

func TestCurrencyConversion(t *testing.T) {
	var disabled *StripeClient
	if disabled.Currency() != "usd" || disabled.USDRate() != 1 {
		t.Fatalf("nil client must default to USD at par")
	}
	if got := disabled.USDFromMinor(2000, "usd", 0); got != 20 {
		t.Fatalf("USD passthrough = %v", got)
	}
	aud := &StripeClient{secretKey: "sk_test", currency: "aud", usdRate: 1.55}
	if got := aud.minorUnits(20); got != 3100 {
		t.Fatalf("$20 at 1.55 = %d minor units, want 3100", got)
	}
	if got := aud.USDFromMinor(3100, "aud", 0); got != 20 {
		t.Fatalf("3100 AUD cents back to USD = %v, want 20", got)
	}
	// The rate recorded on the payment wins over the configured one.
	if got := aud.USDFromMinor(3000, "aud", 1.5); got != 20 {
		t.Fatalf("recorded rate ignored: %v", got)
	}
	// USD amounts never need a rate, even on a non-USD client.
	if got := aud.USDFromMinor(2000, "USD", 0); got != 20 {
		t.Fatalf("legacy USD amount converted: %v", got)
	}
	// Partial amounts round to cents.
	if got := aud.USDFromMinor(775, "aud", 1.55); got != 5 {
		t.Fatalf("775 AUD cents = %v, want 5", got)
	}
}

func TestTopupCheckoutChargesInConfiguredCurrency(t *testing.T) {
	c := &StripeClient{secretKey: "sk_test", currency: "aud", usdRate: 1.55}
	params := &stripe.CheckoutSessionParams{
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripe.String(c.Currency()),
				UnitAmount: stripe.Int64(c.minorUnits(20)),
			},
		}},
	}
	c.addCurrencyMetadata(params.AddMetadata)
	values := &form.Values{}
	form.AppendTo(values, params)
	encoded := values.Encode()
	for _, want := range []string{
		"line_items[0][price_data][currency]=aud",
		"line_items[0][price_data][unit_amount]=3100",
		"metadata[currency]=aud",
		"metadata[usd_rate]=1.55",
	} {
		if !strings.Contains(encoded, want) {
			t.Errorf("%s missing from %q", want, encoded)
		}
	}
}
