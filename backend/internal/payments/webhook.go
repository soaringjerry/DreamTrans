package payments

import (
	"encoding/json"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

func constructEvent(payload []byte, signatureHeader, secret string) (stripe.Event, error) {
	return webhook.ConstructEventWithOptions(payload, signatureHeader, secret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}

// CheckoutCompleted is the subset of checkout.session.completed the ledger needs.
type CheckoutCompleted struct {
	SessionID       string
	Mode            string
	CustomerID      string
	PaymentIntentID string
	SubscriptionID  string
	AmountTotal     int64  // smallest unit of Currency
	Currency        string // lower-case ISO 4217
	PaymentStatus   string // paid | unpaid | no_payment_required
	Metadata        map[string]string
}

// Paid reports whether the session's payment has actually settled. Delayed
// notification methods (bank transfers, some wallets) complete the session
// while still unpaid and settle later via async_payment_succeeded.
func (c *CheckoutCompleted) Paid() bool {
	return c.PaymentStatus != string(stripe.CheckoutSessionPaymentStatusUnpaid)
}

func ParseCheckoutSession(raw json.RawMessage) (CheckoutCompleted, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return CheckoutCompleted{}, err
	}
	result := CheckoutCompleted{
		SessionID: session.ID, Mode: string(session.Mode), PaymentStatus: string(session.PaymentStatus),
		AmountTotal: session.AmountTotal, Currency: string(session.Currency), Metadata: session.Metadata,
	}
	if session.Customer != nil {
		result.CustomerID = session.Customer.ID
	}
	if session.PaymentIntent != nil {
		result.PaymentIntentID = session.PaymentIntent.ID
	}
	if session.Subscription != nil {
		result.SubscriptionID = session.Subscription.ID
	}
	return result, nil
}

func ParseSubscription(raw json.RawMessage) (SubscriptionState, error) {
	var sub stripe.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return SubscriptionState{}, err
	}
	return subscriptionState(&sub), nil
}

// InvoiceEvent is the subset of invoice.* events the ledger needs.
type InvoiceEvent struct {
	InvoiceID      string
	CustomerID     string
	SubscriptionID string
	AmountPaid     int64  // smallest unit of Currency
	Currency       string // lower-case ISO 4217
	BillingReason  string
}

func ParseInvoice(raw json.RawMessage) (InvoiceEvent, error) {
	var invoice stripe.Invoice
	if err := json.Unmarshal(raw, &invoice); err != nil {
		return InvoiceEvent{}, err
	}
	result := InvoiceEvent{
		InvoiceID: invoice.ID, AmountPaid: invoice.AmountPaid, Currency: string(invoice.Currency),
		BillingReason: string(invoice.BillingReason),
	}
	if invoice.Customer != nil {
		result.CustomerID = invoice.Customer.ID
	}
	if invoice.Subscription != nil {
		result.SubscriptionID = invoice.Subscription.ID
	}
	return result, nil
}

// ChargeRefund is the subset of charge.refunded the ledger needs.
type ChargeRefund struct {
	ChargeID        string
	PaymentIntentID string
	Amount          int64 // charge total, smallest unit of Currency
	AmountRefunded  int64 // smallest unit of Currency
	Currency        string
	Refunded        bool // the whole charge has been refunded
	Metadata        map[string]string
	LatestRefundID  string
}

func ParseChargeRefund(raw json.RawMessage) (ChargeRefund, error) {
	var charge stripe.Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		return ChargeRefund{}, err
	}
	result := ChargeRefund{
		ChargeID: charge.ID, Amount: charge.Amount, AmountRefunded: charge.AmountRefunded,
		Currency: string(charge.Currency), Refunded: charge.Refunded, Metadata: charge.Metadata,
	}
	if charge.PaymentIntent != nil {
		result.PaymentIntentID = charge.PaymentIntent.ID
	}
	if charge.Refunds != nil && len(charge.Refunds.Data) > 0 {
		result.LatestRefundID = charge.Refunds.Data[len(charge.Refunds.Data)-1].ID
	}
	if result.LatestRefundID == "" {
		result.LatestRefundID = charge.ID + ":refund"
	}
	return result, nil
}
