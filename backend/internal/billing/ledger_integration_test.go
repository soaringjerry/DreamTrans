package billing

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// integrationDB connects to DREAMTRANS_TEST_DATABASE_URL (a database with all
// migrations applied) or skips.
func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

type integrationUser struct {
	userID, tenantID string
}

func createIntegrationUser(t *testing.T, db *sql.DB, email string) integrationUser {
	t.Helper()
	ctx := t.Context()
	var tenantID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO tenants (name, slug) VALUES ('billing-test', 'billing-test-' || gen_random_uuid()::text)
		RETURNING id
	`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (tenant_id, email, password_hash, name, role)
		VALUES ($1, $2, 'x', 'Billing Test', 'user')
		RETURNING id
	`, tenantID, email+"-"+time.Now().Format("150405.000000")+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return integrationUser{userID: userID, tenantID: tenantID}
}

func newIntegrationService(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	service := NewService(db)
	if err := service.EnsureBuiltinCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSystemSettings(t.Context(), map[string]string{
		"billing_enabled": "true", "allow_negative_balance": "false",
	}, nil); err != nil {
		t.Fatal(err)
	}
	return service
}

func approx(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %.8f, want %.8f", label, got, want)
	}
}

func transcriptionMinutes(user integrationUser, minutes float64, key string) *UsageRecord {
	return &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, Action: "transcription",
		Provider: "speechmatics", Model: RealtimeTranscriptionSKU, Quantity: minutes,
		IdempotencyKey: key,
	}
}

func TestLedgerReserveSettleRefundAcrossBucketsIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "ledger")

	// Fresh account: free plan, empty.
	balance, err := service.GetUserBalance(ctx, user.userID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.PlanCode != FreePlanCode || balance.AvailableUSD != 0 {
		t.Fatalf("new account = %+v", balance)
	}

	// Reserve without funds fails with the sentinel.
	if _, err := service.RecordUsage(ctx, transcriptionMinutes(user, 60, "test:nofunds")); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("empty account reserve err = %v, want ErrInsufficientBalance", err)
	}

	// $0.30 expiring grant + $1.00 wallet.
	expires := time.Now().Add(24 * time.Hour)
	if _, err := service.AddGrant(ctx, GrantInput{UserID: user.userID, Kind: GrantPromo, AmountUSD: 0.30, ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 1.00, Description: "seed"}); err != nil {
		t.Fatal(err)
	}

	// One hour at $0.43 × 1.5 = $0.645: $0.30 from the grant, $0.345 from the wallet.
	cost, err := service.RecordUsage(ctx, transcriptionMinutes(user, 60, "test:hour"))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "reserved charge", cost, 0.645)
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "grant after reserve", balance.GrantUSD, 0)
	approx(t, "wallet after reserve", balance.WalletUSD, 0.655)
	var grantUSD, walletUSD float64
	if err := db.QueryRowContext(ctx, `SELECT grant_usd, wallet_usd FROM usage_logs WHERE idempotency_key = 'test:hour'`).Scan(&grantUSD, &walletUSD); err != nil {
		t.Fatal(err)
	}
	approx(t, "usage grant split", grantUSD, 0.30)
	approx(t, "usage wallet split", walletUSD, 0.345)

	// Duplicate reservation is idempotent: same charge, no second debit.
	duplicate := transcriptionMinutes(user, 60, "test:hour")
	dupCost, err := service.RecordUsage(ctx, duplicate)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "duplicate charge", dupCost, 0.645)
	if !duplicate.IdempotencyDuplicate {
		t.Fatal("duplicate reservation was not flagged")
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after duplicate", balance.WalletUSD, 0.655)

	// Settle at 30 minutes: refund $0.3225 — wallet first ($0.345 cap), so all
	// of it returns to the wallet.
	settled, err := service.SettleUsageReservation(ctx, "test:hour", transcriptionMinutes(user, 30, ""))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "settled charge", settled, 0.3225)
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after settle", balance.WalletUSD, 0.655+0.3225)
	approx(t, "grant after settle", balance.GrantUSD, 0)

	// Settlement is idempotent and refund after settlement is refused.
	if again, err := service.SettleUsageReservation(ctx, "test:hour", transcriptionMinutes(user, 30, "")); err != nil || math.Abs(again-0.3225) > 1e-6 {
		t.Fatalf("second settle = %v, %v", again, err)
	}
	if err := service.RefundUsage(ctx, "test:hour", "late"); !errors.Is(err, ErrReservationSettled) {
		t.Fatalf("refund after settle err = %v, want ErrReservationSettled", err)
	}

	// A refunded reservation restores the grant lot it consumed.
	if _, err := service.RecordUsage(ctx, transcriptionMinutes(user, 10, "test:refund")); err != nil {
		t.Fatal(err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after second reserve", balance.WalletUSD, 0.9775-0.1075)
	if err := service.RefundUsage(ctx, "test:refund", "provider failed"); err != nil {
		t.Fatal(err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after refund", balance.WalletUSD, 0.9775)
	if err := service.RefundUsage(ctx, "test:refund", "again"); err != nil {
		t.Fatalf("second refund must be idempotent: %v", err)
	}
	if err := service.RefundUsage(ctx, "test:never", "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown key refund err = %v, want sql.ErrNoRows", err)
	}

	// Ledger rows are per bucket and reconcile to the balances.
	history, err := service.GetBalanceHistory(ctx, user.userID, 50)
	if err != nil {
		t.Fatal(err)
	}
	wallet, grant := 0.0, 0.0
	for _, entry := range history {
		switch entry.Bucket {
		case BucketWallet:
			wallet += entry.AmountUSD
		case BucketGrant:
			grant += entry.AmountUSD
		}
	}
	approx(t, "ledger wallet sum", wallet, balance.WalletUSD)
	approx(t, "ledger grant sum", grant, 0.30-0.30)
}

func TestLedgerMembershipDiscountAndBYOKIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "member")
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 10, Description: "seed"}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * 24 * time.Hour)
	if _, err := service.SetAccountPlan(ctx, PlanAssignment{UserID: user.userID, PlanCode: "pro", MemberUntil: &until, Actor: ""}); err != nil {
		t.Fatal(err)
	}
	// Pro: 20% off $0.645 = $0.516 per hour.
	cost, err := service.RecordUsage(ctx, transcriptionMinutes(user, 60, "member:hour"))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "member hourly charge", cost, 0.516)
	summary, err := service.GetAccountSummary(ctx, user.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.MemberActive || summary.EffectivePlan.Code != "pro" || summary.DiscountPercent != 20 {
		t.Fatalf("summary = plan %s active %v discount %v", summary.EffectivePlan.Code, summary.MemberActive, summary.DiscountPercent)
	}
	approx(t, "member hourly example", summary.RealtimeHourUSD, 0.516)

	// Settling after the membership lapsed still uses the snapshot discount.
	if _, err := db.ExecContext(ctx, `UPDATE billing_accounts SET member_until = NOW() - INTERVAL '1 day' WHERE id = $1`, summary.AccountID); err != nil {
		t.Fatal(err)
	}
	settled, err := service.SettleUsageReservation(ctx, "member:hour", transcriptionMinutes(user, 120, ""))
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "settled with snapshot discount", settled, 1.032)
	plan, err := service.EffectivePlan(ctx, user.userID)
	if err != nil || plan.Code != FreePlanCode {
		t.Fatalf("lapsed member effective plan = %v, %v", plan, err)
	}

	// BYOK usage is free and records no upstream cost.
	byok := &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, Action: "translation",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 1000, OutputTokens: 200,
		CustomerFunded: true, IdempotencyKey: "member:byok",
	}
	cost, err = service.RecordUsage(ctx, byok)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0 {
		t.Fatalf("BYOK charge = %v, want 0", cost)
	}
	var attribution string
	var upstream float64
	if err := db.QueryRowContext(ctx, `SELECT cost_attribution, upstream_cost_usd FROM usage_logs WHERE idempotency_key = 'member:byok'`).Scan(&attribution, &upstream); err != nil {
		t.Fatal(err)
	}
	if attribution != AttributionBYOK || upstream != 0 {
		t.Fatalf("BYOK row = %s / %v", attribution, upstream)
	}
}

func TestLedgerSettlementShortfallPolicyIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "shortfall")
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 0.20, Description: "seed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordUsage(ctx, transcriptionMinutes(user, 10, "short:one")); err != nil {
		t.Fatal(err)
	}
	// Actual 60 minutes costs $0.645 but only $0.0925 remains.
	if _, err := service.SettleUsageReservation(ctx, "short:one", transcriptionMinutes(user, 60, "")); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("strict settlement err = %v, want ErrInsufficientBalance", err)
	}
	var settledAt sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT settled_at FROM usage_logs WHERE idempotency_key = 'short:one'`).Scan(&settledAt); err != nil {
		t.Fatal(err)
	}
	if settledAt.Valid {
		t.Fatal("failed strict settlement must leave the reservation open")
	}
	absorbing := transcriptionMinutes(user, 60, "")
	absorbing.AbsorbSettlementShortfall = true
	charge, err := service.SettleUsageReservation(ctx, "short:one", absorbing)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "absorbed charge stays at reservation", charge, 0.1075)
	var margin float64
	if err := db.QueryRowContext(ctx, `SELECT margin_usd FROM usage_logs WHERE idempotency_key = 'short:one'`).Scan(&margin); err != nil {
		t.Fatal(err)
	}
	// charge 0.1075 − upstream 0.43 = −0.3225 (the shortfall is visible as negative margin).
	approx(t, "absorbed margin", margin, 0.1075-0.43)
}

func TestTopupTrialAndMembershipLifecycleIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "topup")
	if err := service.SetSystemSettings(ctx, map[string]string{"trial_credit_usd": "1", "trial_credit_days": "30"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.GrantTrialCredit(ctx, user.userID); err != nil {
		t.Fatal(err)
	}
	if err := service.GrantTrialCredit(ctx, user.userID); err != nil {
		t.Fatal(err)
	}
	balance, _ := service.GetUserBalance(ctx, user.userID)
	approx(t, "trial granted once", balance.GrantUSD, 1)

	if _, err := service.RecordTopup(ctx, TopupInput{UserID: user.userID, AmountUSD: 20, BonusUSD: 2, BonusExpiryDays: 365, StripeObjectID: "cs_test_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordTopup(ctx, TopupInput{UserID: user.userID, AmountUSD: 20, BonusUSD: 2, StripeObjectID: "cs_test_1"}); !errors.Is(err, ErrDuplicatePayment) {
		t.Fatalf("replayed top-up err = %v, want ErrDuplicatePayment", err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after top-up", balance.WalletUSD, 20)
	approx(t, "grants after top-up", balance.GrantUSD, 3)

	// Membership from a Stripe subscription, renewal, then termination.
	start := time.Now().UTC()
	end := start.Add(30 * 24 * time.Hour)
	summary, err := service.ApplyMembership(ctx, MembershipInput{
		UserID: user.userID, PlanCode: "pro", Interval: "month", StripeSubscriptionID: "sub_test_1",
		Status: "active", CurrentPeriodStart: &start, CurrentPeriodEnd: &end, PaidAmountUSD: 6, StripeInvoiceID: "in_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.MemberActive || summary.Membership == nil || summary.Membership.StripeSubscriptionID != "sub_test_1" {
		t.Fatalf("membership not active: %+v", summary.AccountBalance)
	}
	userID, err := service.UserIDBySubscription(ctx, "sub_test_1")
	if err != nil || userID != user.userID {
		t.Fatalf("subscription lookup = %q, %v", userID, err)
	}
	if err := service.EndMembership(ctx, "sub_test_1"); err != nil {
		t.Fatal(err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	if balance.MemberActive || balance.PlanCode != FreePlanCode {
		t.Fatalf("ended membership balance = %+v", balance)
	}
	payments, err := service.ListPayments(ctx, user.userID, 10)
	if err != nil || len(payments) != 2 {
		t.Fatalf("payments = %d, %v", len(payments), err)
	}

	// A Stripe refund of the top-up revokes the unused bonus and debits the wallet.
	if err := service.RecordPaymentRefund(ctx, "cs_test_1", 20, "re_1"); err != nil {
		t.Fatal(err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after refund", balance.WalletUSD, 0)
	approx(t, "grants after bonus revoke", balance.GrantUSD, 1)
}

func TestTranslationReplaySettlesThroughSharedLedgerIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "replay")
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 1, Description: "seed"}); err != nil {
		t.Fatal(err)
	}
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	requestKey := "ws-translation:test-" + time.Now().Format("150405.000000")
	owner := &UsageRecord{UserID: user.userID, TenantID: user.tenantID, Action: "translation"}
	claim, err := service.ClaimTranslationRequest(ctx, requestKey, fingerprint, owner, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Disposition != TranslationRequestOwner {
		t.Fatalf("claim = %+v", claim)
	}
	reservation := &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, Action: "translation",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 100_000, OutputTokens: 10_000,
		IdempotencyKey: claim.UsageIdempotencyKey,
	}
	reserved, err := service.RecordUsage(ctx, reservation)
	if err != nil {
		t.Fatal(err)
	}
	// 100k × 0.2e-6 + 10k × 1.2e-6 = 0.032 upstream × 1.5 = 0.048
	approx(t, "translation reservation", reserved, 0.048)
	actual := &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, Action: "translation",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 50_000, OutputTokens: 5_000,
	}
	charge, err := service.SettleTranslationRequest(ctx, requestKey, claim.Attempt, claim.UsageIdempotencyKey, actual,
		&TranslationReplayResult{Content: "你好", Model: "gpt-5.6-luna", LatencyMs: 10}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "translation settlement", charge, 0.024)
	replay, err := service.ClaimTranslationRequest(ctx, requestKey, fingerprint, owner, time.Minute, time.Hour)
	if err != nil || replay.Disposition != TranslationRequestReplay || replay.Result.Content != "你好" {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	balance, _ := service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after translation", balance.WalletUSD, 1-0.024)

	// A failed attempt refunds and the next claim gets a fresh attempt key.
	requestKey2 := requestKey + "-b"
	claim2, err := service.ClaimTranslationRequest(ctx, requestKey2, fingerprint, owner, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reservation.IdempotencyKey = claim2.UsageIdempotencyKey
	if _, err := service.RecordUsage(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if err := service.FailTranslationRequest(ctx, requestKey2, claim2.Attempt, claim2.UsageIdempotencyKey, "provider failed"); err != nil {
		t.Fatal(err)
	}
	balance, _ = service.GetUserBalance(ctx, user.userID)
	approx(t, "wallet after failed attempt refund", balance.WalletUSD, 1-0.024)
	claim3, err := service.ClaimTranslationRequest(ctx, requestKey2, fingerprint, owner, time.Minute, time.Hour)
	if err != nil || claim3.Disposition != TranslationRequestOwner || claim3.Attempt != 2 {
		t.Fatalf("retry claim = %+v, %v", claim3, err)
	}
}

func TestPricingUnitTests(t *testing.T) {
	provider, sku := CanonicalSKU("", "speechmatics-classic-token", "transcription")
	if provider != "speechmatics" || sku != RealtimeTranscriptionSKU {
		t.Fatalf("alias = %s/%s", provider, sku)
	}
	retail, charge := retailFromUpstream(0.43, 50, 20)
	approx(t, "retail", retail, 0.645)
	approx(t, "member charge", charge, 0.516)
	if _, _, err := retailFromUpstreamCheck(); err != nil {
		t.Fatal(err)
	}
}

func retailFromUpstreamCheck() (float64, float64, error) {
	view := &usagePricingView{Config: BillingConfig{DefaultMarkupPercent: 50}}
	for _, rate := range builtinCostRates {
		rate.EffectiveCostPerUnitUSD = rate.CostPerUnitUSD
		rate.IsActive = true
		view.Rates = append(view.Rates, rate)
	}
	rec := &UsageRecord{Action: "transcription", Provider: "speechmatics", Model: RealtimeTranscriptionSKU, Quantity: 60}
	breakdown, err := priceUsage(rec, view, accountPricing{PlanCode: "pro", DiscountPercent: 20})
	if err != nil {
		return 0, 0, err
	}
	if math.Abs(breakdown.ChargeUSD-0.516) > 1e-9 || math.Abs(breakdown.UpstreamUSD-0.43) > 1e-9 {
		return 0, 0, errors.New("priceUsage produced unexpected numbers")
	}
	resolved, err := resolveUsageCostFromSnapshot(breakdown.Snapshot, &UsageRecord{Action: "transcription", Quantity: 30}, AttributionProviderPriced)
	if err != nil {
		return 0, 0, err
	}
	if math.Abs(resolved.ChargeUSD-0.258) > 1e-9 {
		return 0, 0, errors.New("snapshot repricing produced unexpected numbers")
	}
	return breakdown.ChargeUSD, resolved.ChargeUSD, nil
}
