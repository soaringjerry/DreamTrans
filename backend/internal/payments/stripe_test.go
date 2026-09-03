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
