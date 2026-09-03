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
