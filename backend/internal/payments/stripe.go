// Package payments wraps the Stripe API surface DreamTrans uses: hosted
// Checkout for top-ups and memberships, the customer portal, off-session
// charges for automatic top-up, and webhook signature verification.
package payments

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/stripe/stripe-go/v81"
	portalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/paymentintent"
	"github.com/stripe/stripe-go/v81/paymentmethod"
	"github.com/stripe/stripe-go/v81/subscription"
)

// ErrNotConfigured is returned when Stripe keys are absent. Callers should
// degrade to "payments unavailable" rather than fail startup.
var ErrNotConfigured = errors.New("stripe is not configured")

// StripeClient is safe for concurrent use. A nil *StripeClient is a valid
// "disabled" client.
type StripeClient struct {
	secretKey     string
	webhookSecret string
}

// NewStripeFromEnv reads STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET. It
// returns (nil, nil) when the secret key is unset so deployments without
// payments keep working.
func NewStripeFromEnv() (*StripeClient, error) {
	key := strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
	if key == "" {
		return nil, nil
	}
	if !strings.HasPrefix(key, "sk_") && !strings.HasPrefix(key, "rk_") {
		return nil, fmt.Errorf("STRIPE_SECRET_KEY does not look like a Stripe secret key")
	}
	stripe.Key = key
	stripe.SetAppInfo(&stripe.AppInfo{Name: "DreamTrans", URL: "https://github.com/soaringjerry/DreamTrans"})
	return &StripeClient{
		secretKey:     key,
		webhookSecret: strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
	}, nil
}

func (c *StripeClient) Enabled() bool { return c != nil && c.secretKey != "" }

func (c *StripeClient) WebhookConfigured() bool { return c != nil && c.webhookSecret != "" }

func cents(usd float64) int64 {
	return int64(math.Round(usd * 100))
}

// EnsureCustomer returns an existing Stripe customer id or creates one.
func (c *StripeClient) EnsureCustomer(ctx context.Context, existingID, email, userID string) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}
	if strings.TrimSpace(existingID) != "" {
		return existingID, nil
	}
	params := &stripe.CustomerParams{Email: stripe.String(email)}
	params.Context = ctx
	params.AddMetadata("user_id", userID)
	created, err := customer.New(params)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// CheckoutInput describes one hosted Checkout session.
type CheckoutInput struct {
	CustomerID string
	UserID     string
	SuccessURL string
	CancelURL  string
	// Top-up
	AmountUSD       float64
	BonusUSD        float64
	BonusExpiryDays int
	SaveCard        bool
	// Membership
	PlanCode     string
	PlanName     string
	Interval     string // month | year
	PriceUSD     float64
	StripePrice  string // optional pre-created Price id
	ProductLabel string
}

// CreateTopupCheckout starts a one-time payment for a wallet top-up. The
// bonus is carried in metadata so the webhook grants exactly what was shown.
func (c *StripeClient) CreateTopupCheckout(ctx context.Context, input *CheckoutInput) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}
	label := input.ProductLabel
	if label == "" {
		label = fmt.Sprintf("DreamTrans wallet top-up $%.2f", input.AmountUSD)
	}
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		Customer:   stripe.String(input.CustomerID),
		SuccessURL: stripe.String(input.SuccessURL),
		CancelURL:  stripe.String(input.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripe.String("usd"),
				UnitAmount: stripe.Int64(cents(input.AmountUSD)),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
					Name: stripe.String(label),
				},
			},
		}},
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{},
	}
	params.Context = ctx
	params.AddMetadata("kind", "topup")
	params.AddMetadata("user_id", input.UserID)
	params.AddMetadata("amount_usd", strconv.FormatFloat(input.AmountUSD, 'f', 2, 64))
	params.AddMetadata("bonus_usd", strconv.FormatFloat(input.BonusUSD, 'f', 2, 64))
	params.AddMetadata("bonus_days", strconv.Itoa(input.BonusExpiryDays))
	params.PaymentIntentData.AddMetadata("kind", "topup")
	params.PaymentIntentData.AddMetadata("user_id", input.UserID)
	if input.SaveCard {
		params.PaymentIntentData.SetupFutureUsage = stripe.String("off_session")
	}
	session, err := checkoutsession.New(params)
	if err != nil {
		return "", err
	}
	return session.URL, nil
}

// CreateMembershipCheckout starts a subscription. When the plan has no
// pre-created Stripe Price, an ad-hoc recurring price is used so no dashboard
// setup is required.
func (c *StripeClient) CreateMembershipCheckout(ctx context.Context, input *CheckoutInput) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}
	interval := "month"
	if input.Interval == "year" {
		interval = "year"
	}
	item := &stripe.CheckoutSessionLineItemParams{Quantity: stripe.Int64(1)}
	if strings.TrimSpace(input.StripePrice) != "" {
		item.Price = stripe.String(strings.TrimSpace(input.StripePrice))
	} else {
		name := input.PlanName
		if name == "" {
			name = input.PlanCode
		}
		item.PriceData = &stripe.CheckoutSessionLineItemPriceDataParams{
			Currency:   stripe.String("usd"),
			UnitAmount: stripe.Int64(cents(input.PriceUSD)),
			Recurring:  &stripe.CheckoutSessionLineItemPriceDataRecurringParams{Interval: stripe.String(interval)},
			ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
				Name: stripe.String("DreamTrans " + name + " membership"),
			},
		}
	}
	params := &stripe.CheckoutSessionParams{
		Mode:             stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:         stripe.String(input.CustomerID),
		SuccessURL:       stripe.String(input.SuccessURL),
		CancelURL:        stripe.String(input.CancelURL),
		LineItems:        []*stripe.CheckoutSessionLineItemParams{item},
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{},
	}
	params.Context = ctx
	params.AddMetadata("kind", "membership")
	params.AddMetadata("user_id", input.UserID)
	params.AddMetadata("plan_code", input.PlanCode)
	params.AddMetadata("interval", interval)
	params.SubscriptionData.AddMetadata("user_id", input.UserID)
	params.SubscriptionData.AddMetadata("plan_code", input.PlanCode)
	params.SubscriptionData.AddMetadata("interval", interval)
	session, err := checkoutsession.New(params)
	if err != nil {
		return "", err
	}
	return session.URL, nil
}

// CreatePortalSession opens Stripe's hosted customer portal (cancel, change
// card, invoices).
func (c *StripeClient) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}
	params.Context = ctx
	session, err := portalsession.New(params)
	if err != nil {
		return "", err
	}
	return session.URL, nil
}

// SubscriptionState is the subset of a Stripe subscription the ledger needs.
type SubscriptionState struct {
	ID                 string
	CustomerID         string
	Status             string
	Interval           string
	PlanCode           string
	UserID             string
	CurrentPeriodStart int64
	CurrentPeriodEnd   int64
	CancelAtPeriodEnd  bool
}

func subscriptionState(sub *stripe.Subscription) SubscriptionState {
	state := SubscriptionState{
		ID: sub.ID, Status: string(sub.Status),
		CurrentPeriodStart: sub.CurrentPeriodStart, CurrentPeriodEnd: sub.CurrentPeriodEnd,
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
	}
	if sub.Customer != nil {
		state.CustomerID = sub.Customer.ID
	}
	if sub.Metadata != nil {
		state.PlanCode = sub.Metadata["plan_code"]
		state.UserID = sub.Metadata["user_id"]
		state.Interval = sub.Metadata["interval"]
	}
	if sub.Items != nil && len(sub.Items.Data) > 0 && sub.Items.Data[0].Price != nil &&
		sub.Items.Data[0].Price.Recurring != nil {
		state.Interval = string(sub.Items.Data[0].Price.Recurring.Interval)
	}
	return state
}

// SubscriptionFromObject maps a webhook payload subscription.
func SubscriptionFromObject(sub *stripe.Subscription) SubscriptionState {
	return subscriptionState(sub)
}

// GetSubscription fetches the current subscription state from Stripe.
func (c *StripeClient) GetSubscription(ctx context.Context, subscriptionID string) (SubscriptionState, error) {
	if !c.Enabled() {
		return SubscriptionState{}, ErrNotConfigured
	}
	params := &stripe.SubscriptionParams{}
	params.Context = ctx
	sub, err := subscription.Get(subscriptionID, params)
	if err != nil {
		return SubscriptionState{}, err
	}
	return subscriptionState(sub), nil
}

// ChargeOffSession charges the customer's saved card for an automatic
// top-up. idempotencyKey must be stable for one logical top-up attempt so a
// retried call cannot charge twice.
func (c *StripeClient) ChargeOffSession(ctx context.Context, customerID string, amountUSD float64, description, idempotencyKey string) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}
	listParams := &stripe.PaymentMethodListParams{
		Customer: stripe.String(customerID),
		Type:     stripe.String("card"),
	}
	listParams.Context = ctx
	listParams.Limit = stripe.Int64(1)
	iter := paymentmethod.List(listParams)
	var methodID string
	for iter.Next() {
		methodID = iter.PaymentMethod().ID
		break
	}
	if err := iter.Err(); err != nil {
		return "", err
	}
	if methodID == "" {
		return "", fmt.Errorf("no saved payment method for automatic top-up")
	}
	params := &stripe.PaymentIntentParams{
		Amount:        stripe.Int64(cents(amountUSD)),
		Currency:      stripe.String("usd"),
		Customer:      stripe.String(customerID),
		PaymentMethod: stripe.String(methodID),
		Confirm:       stripe.Bool(true),
		OffSession:    stripe.Bool(true),
		Description:   stripe.String(description),
	}
	params.Context = ctx
	params.AddMetadata("kind", "auto_topup")
	params.SetIdempotencyKey(idempotencyKey)
	intent, err := paymentintent.New(params)
	if err != nil {
		return "", err
	}
	if intent.Status != stripe.PaymentIntentStatusSucceeded {
		return "", fmt.Errorf("automatic top-up payment is %s", intent.Status)
	}
	return intent.ID, nil
}

// Event re-exports the Stripe event type for handlers.
type Event = stripe.Event

// ConstructEvent verifies the webhook signature.
func (c *StripeClient) ConstructEvent(payload []byte, signatureHeader string) (Event, error) {
	if !c.WebhookConfigured() {
		return Event{}, fmt.Errorf("%w: STRIPE_WEBHOOK_SECRET is unset", ErrNotConfigured)
	}
	return constructEvent(payload, signatureHeader, c.webhookSecret)
}
