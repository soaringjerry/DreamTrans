package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/dreamtrans/backend/internal/risk"
)

func TestSignupDefenseStrictBudgetConcurrencyAndRecovery(t *testing.T) {
	h, _, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 3)
	service := setupSignupRisk(t, h)
	var actor string
	if err := db.QueryRowContext(t.Context(), `SELECT created_by FROM promotion_invites WHERE id=$1`, offer.ID).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	before, err := service.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.UpdateSettings(context.Background(), before, actor) })
	settings := before
	settings.StrictMode = true
	settings.Enabled = false
	var spentCents int64
	if err = db.QueryRowContext(t.Context(), `SELECT CEIL(COALESCE(SUM(amount_usd),0)*100)::bigint FROM signup_risk_reward_spend WHERE created_at>NOW()-INTERVAL '24 hours'`).Scan(&spentCents); err != nil {
		t.Fatal(err)
	}
	settings.DailyRewardBudgetCents = spentCents + 250
	if err = service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	users := []string{}
	for i := 0; i < 2; i++ {
		email := uniqueEmail(t, fmt.Sprintf("strict%d", i))
		response := riskSignup(t, h, email, offer.Code, signupDevice(t, h), fmt.Sprintf("203.0.%d.10", i))
		if response.Code != 202 || !strings.Contains(response.Body.String(), `"reward_review_required":true`) {
			t.Fatalf("strict bypass: %s", response.Body.String())
		}
		u, err := h.store.GetUserByEmail(t.Context(), email)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u.ID)
		if _, err = db.ExecContext(t.Context(), `UPDATE users SET email_verified=TRUE WHERE id=$1`, u.ID); err != nil {
			t.Fatal(err)
		}
		if err = h.billing.GrantPromotionRewards(t.Context(), u.ID); err != nil {
			t.Fatal(err)
		}
		balance, err := h.billing.GetUserBalance(t.Context(), u.ID)
		if err != nil || balance.GrantUSD != 0 || balance.PlanCode != "free" {
			t.Fatalf("strict reward leak: %+v %v", balance, err)
		}
		profiles, _, err := service.List(t.Context(), "review", email, 20, 0)
		if err != nil || len(profiles) != 1 {
			t.Fatal("missing strict review", err)
		}
		if _, err = service.Review(t.Context(), profiles[0].ID, "approved", "Identity checked", actor); err != nil {
			t.Fatal(err)
		}
	}
	// A blocked trial and a smaller successful promotion have independent holds.
	trialTx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	trialOK, err := risk.ReserveRewardTx(t.Context(), trialTx, users[0], "trial", 999999)
	if err != nil || trialOK {
		_ = trialTx.Rollback()
		t.Fatal("expected trial hold", err)
	}
	if err = trialTx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A reservation in a failed transaction must not consume the budget.
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := risk.ReserveRewardTx(t.Context(), tx, users[0], "promotion", 2.5)
	if err != nil || !ok {
		_ = tx.Rollback()
		t.Fatal("reservation failed", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range users {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- h.billing.GrantPromotionRewards(t.Context(), id) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var total float64
	if err = db.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(amount_usd),0) FROM signup_risk_reward_spend WHERE split_part(receipt_key,':',1) IN ($1,$2)`, users[0], users[1]).Scan(&total); err != nil || total != 2.5 {
		t.Fatalf("concurrent overspend: %f %v", total, err)
	}
	blocked := ""
	for _, id := range users {
		status, err := service.UserDecision(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if status == "budget_hold" {
			b, err := h.billing.GetUserBalance(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if b.GrantUSD == 0 {
				blocked = id
			}
		}
	}
	if blocked == "" {
		t.Fatal("no budget hold reported")
	}
	// Approval retries cannot bypass the cap; increased budget enables recovery.
	if err = h.billing.GrantPromotionRewards(t.Context(), blocked); err != nil {
		t.Fatal(err)
	}
	balance, err := h.billing.GetUserBalance(t.Context(), blocked)
	if err != nil || balance.GrantUSD != 0 || balance.PlanCode != "free" {
		t.Fatalf("budget bypass: %+v %v", balance, err)
	}
	settings.DailyRewardBudgetCents += 250
	if err = service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = h.billing.GrantPromotionRewards(t.Context(), blocked); err != nil {
			t.Fatal(err)
		}
	}
	balance, err = h.billing.GetUserBalance(t.Context(), blocked)
	if err != nil || balance.GrantUSD != 2.5 || balance.PlanCode != "pro" {
		t.Fatalf("recovery: %+v %v", balance, err)
	}
	trialStatus, err := service.UserDecision(t.Context(), users[0])
	if err != nil || trialStatus != "budget_hold" {
		t.Fatalf("promotion erased trial hold: %s %v", trialStatus, err)
	}
	// Clear the separate trial hold with a zero-credit trial budget receipt.
	clearTx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	trialOK, err = risk.ReserveRewardTx(t.Context(), clearTx, users[0], "trial", 0)
	if err != nil || !trialOK {
		_ = clearTx.Rollback()
		t.Fatal("clear trial hold", err)
	}
	if err = clearTx.Commit(); err != nil {
		t.Fatal(err)
	}
	status, err := service.UserDecision(t.Context(), blocked)
	if err != nil || status != "approved" {
		t.Fatalf("status %s %v", status, err)
	}
}

func TestSignupDefenseCookieResetAndRotatingIPCorrelation(t *testing.T) {
	h, _, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 5)
	service := setupSignupRisk(t, h)
	var actor string
	_ = db.QueryRowContext(t.Context(), `SELECT created_by FROM promotion_invites WHERE id=$1`, offer.ID).Scan(&actor)
	before, err := service.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.UpdateSettings(context.Background(), before, actor) })
	settings := before
	settings.StrictMode = true
	settings.NetworkBurstLimit = 1
	settings.PrefixHourlyLimit = 2
	if err = service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	for i, ip := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12", "192.0.2.12"} {
		email := uniqueEmail(t, fmt.Sprintf("cohort%d", i))
		r := riskSignup(t, h, email, offer.Code, signupDevice(t, h), ip)
		if r.Code != 202 {
			t.Fatalf("registration: %s", r.Body.String())
		}
		profiles, _, err := service.List(t.Context(), "review", email, 20, 0)
		if err != nil || len(profiles) != 1 {
			t.Fatal(err)
		}
		p := profiles[0]
		if i == 0 {
			if _, err = service.Review(t.Context(), p.ID, "denied", "Confirmed campaign abuse", actor); err != nil {
				t.Fatal(err)
			}
		}
		if i >= 2 {
			joined := strings.Join(p.Reasons, ",")
			if !strings.Contains(joined, "fingerprint_cluster") || !strings.Contains(joined, "prefix_velocity") || !strings.Contains(joined, "linked_denied") || p.Score < 60 {
				t.Fatalf("missed rotating-IP cohort: %+v", p)
			}
		}
		if i == 3 && !strings.Contains(strings.Join(p.Reasons, ","), "network_burst") {
			t.Fatal("missed short IP burst")
		}
	}
}
