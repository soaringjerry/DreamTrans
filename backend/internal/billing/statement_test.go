package billing

import (
	"math"
	"testing"
)

func near(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func TestStatementTotalsSplitsChargesLikeASession(t *testing.T) {
	usage := []UserUsageItem{
		{Action: "transcription", Quantity: 104, CostUSD: 1.66, WalletUSD: 1.66},
		{Action: "translation", CostUSD: 0.14, WalletUSD: 0.10, GrantUSD: 0.04},
		{Action: "chat", CostUSD: 0.004, GrantUSD: 0.004},
		{Action: "summarize", CostUSD: 0.002, GrantUSD: 0.002},
		// A refunded row is reported, but must not inflate what was spent.
		{Action: "transcription", Quantity: 10, CostUSD: 0.16, WalletUSD: 0.16, Refunded: true},
	}
	payments := []PaymentRow{
		{Kind: "topup", AmountUSD: 20, Status: "succeeded"},
		{Kind: "membership", AmountUSD: 12, Status: "succeeded"},
		{Kind: "topup", AmountUSD: 50, Status: "failed"},
	}
	totals := statementTotals(usage, payments)

	near(t, "transcription", totals.TranscriptionUSD, 1.66)
	near(t, "transcription seconds", totals.TranscriptionSeconds, 104*60)
	near(t, "translation", totals.TranslationUSD, 0.14)
	near(t, "ai", totals.AIUSD, 0.006)
	near(t, "charged", totals.ChargedUSD, 1.806)
	near(t, "refunded", totals.RefundedUSD, 0.16)
	near(t, "from grant", totals.FromGrantUSD, 0.046)
	near(t, "from wallet", totals.FromWalletUSD, 1.76)
	near(t, "topup", totals.TopupUSD, 20)
	near(t, "membership", totals.MembershipUSD, 12)

	near(t, "buckets sum to charged",
		totals.TranscriptionUSD+totals.TranslationUSD+totals.AIUSD, totals.ChargedUSD)
}
