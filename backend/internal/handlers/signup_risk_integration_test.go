package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/risk"
)

func setupSignupRisk(t *testing.T, h *AuthHandler) *risk.Service {
	t.Helper()
	service := risk.NewService(h.store.DB())
	detector, err := risk.NewDetector(strings.Repeat("r", 32), auth.NewAPIGuard(nil).ClientIP)
	if err != nil {
		t.Fatal(err)
	}
	h.SetSignupRisk(detector, service)
	h.billing = billing.NewService(h.store.DB())

	return service
}
func signupDevice(t *testing.T, h *AuthHandler) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	h.HandleSignupContext(w, httptest.NewRequest(http.MethodGet, "https://app.example.test/api/auth/signup-context", nil))
	if w.Code != 200 {
		t.Fatalf("context %d", w.Code)
	}
	return w.Result().Cookies()[0]
}
func riskSignup(t *testing.T, h *AuthHandler, email, invite string, cookie *http.Cookie, ip string) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse battery", "name": "Risk Test", "invite_code": invite})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(data))
	r.RemoteAddr = ip + ":12345"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.HandleRegister(w, r)
	return w
}
func riskAdminReview(t *testing.T, h *AuthHandler, id, actor, decision string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/signup-risk/"+id, strings.NewReader(`{"decision":"`+decision+`","note":"Confirmed by support"}`))
	r = r.WithContext(context.WithValue(r.Context(), auth.UserClaimsKey, &auth.UserClaims{Role: "super_admin", UserID: actor}))
	w := httptest.NewRecorder()
	NewAdminHandler(h.store, h.billing).HandleSignupRisk(w, r)
	return w
}
func TestSignupRiskHoldsRewardsAndAdminApprovalReleases(t *testing.T) {
	h, mail, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 10)
	service := setupSignupRisk(t, h)
	cookie := signupDevice(t, h)
	for i, prefix := range []string{"first", "second"} {
		email := uniqueEmail(t, prefix)
		registered := riskSignup(t, h, email, offer.Code, cookie, "198.51.100.10")
		if registered.Code != 202 {
			t.Fatalf("register: %d %s", registered.Code, registered.Body.String())
		}
		var pending RegistrationPendingResponse
		_ = json.Unmarshal(registered.Body.Bytes(), &pending)
		if pending.RewardReviewRequired != (i == 1) {
			t.Fatalf("review flag %d %+v", i, pending)
		}
		token := verifyLinkPattern.FindStringSubmatch(mail.last(t).Text)[1]
		verified := postJSON(t, h.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": token})
		if verified.Code != 200 {
			t.Fatalf("verify %d %s", verified.Code, verified.Body.String())
		}
		u, _ := h.store.GetUserByEmail(t.Context(), email)
		balance, err := h.billing.GetUserBalance(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if balance.PlanCode != "pro" || balance.GrantUSD < 2.5 {
				t.Fatalf("first rewards: %+v", balance)
			}
			continue
		}
		if balance.PlanCode != "free" || balance.GrantUSD != 0 {
			t.Fatalf("held rewards leaked: %+v", balance)
		}
		login := postJSON(t, h.HandleLogin, "/api/auth/login", map[string]any{"email": email, "password": "correct horse battery"})
		if login.Code != 200 {
			t.Fatal("risk hold blocked login")
		}
		profiles, total, err := service.List(t.Context(), "review", email, 20, 0)
		if err != nil || total != 1 {
			t.Fatalf("review queue: %d %v", total, err)
		}
		var actor string
		if err := db.QueryRowContext(t.Context(), `SELECT created_by FROM promotion_invites WHERE id=$1`, offer.ID).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 2; j++ {
			response := riskAdminReview(t, h, profiles[0].ID, actor, "approved")
			if response.Code != 200 {
				t.Fatalf("approval: %d %s", response.Code, response.Body.String())
			}
		}
		balance, err = h.billing.GetUserBalance(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if balance.PlanCode != "pro" || balance.GrantUSD < 2.5 {
			t.Fatalf("approved rewards: %+v", balance)
		}
		entries, err := service.Audit(t.Context(), profiles[0].ID)
		if err != nil || len(entries) != 1 {
			t.Fatalf("idempotent audit history: %+v %v", entries, err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM grants WHERE account_id=$1 AND kind='promo'`, balance.AccountID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("duplicate promotion grants: %d", count)
		}
		revoked := riskAdminReview(t, h, profiles[0].ID, actor, "denied")
		if revoked.Code != 409 {
			t.Fatalf("retroactive denial: %d", revoked.Code)
		}
	}
}
func TestSignupRiskMissingDeviceDeniedAndPreVerificationApproval(t *testing.T) {
	h, mail, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 5)
	service := setupSignupRisk(t, h)
	for _, decision := range []string{"denied", "approved"} {
		email := uniqueEmail(t, decision)
		registered := riskSignup(t, h, email, offer.Code, nil, "198.51.100.20")
		if registered.Code != 202 || !strings.Contains(registered.Body.String(), `"reward_review_required":true`) {
			t.Fatalf("missing-device signup: %s", registered.Body.String())
		}
		profiles, _, err := service.List(t.Context(), "review", email, 20, 0)
		if err != nil || len(profiles) != 1 {
			t.Fatalf("review profiles %v %v", profiles, err)
		}
		var actor string
		_ = db.QueryRowContext(t.Context(), `SELECT created_by FROM promotion_invites WHERE id=$1`, offer.ID).Scan(&actor)
		response := riskAdminReview(t, h, profiles[0].ID, actor, decision)
		if response.Code != 200 {
			t.Fatalf("decision: %d %s", response.Code, response.Body.String())
		}
		u, _ := h.store.GetUserByEmail(t.Context(), email)
		balance, _ := h.billing.GetUserBalance(t.Context(), u.ID)
		if balance.GrantUSD != 0 {
			t.Fatal("approval bypassed email verification")
		}
		token := verifyLinkPattern.FindStringSubmatch(mail.last(t).Text)[1]
		verified := postJSON(t, h.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": token})
		if verified.Code != 200 {
			t.Fatalf("verify: %s", verified.Body.String())
		}
		balance, _ = h.billing.GetUserBalance(t.Context(), u.ID)
		if decision == "denied" && balance.GrantUSD != 0 {
			t.Fatal("denied rewards leaked")
		}
		if decision == "approved" && balance.PlanCode != "pro" {
			t.Fatal("approved gift missing")
		}
	}
}
func TestSignupRiskConcurrentDeviceRegistrations(t *testing.T) {
	h, _, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 5)
	_ = setupSignupRisk(t, h)
	cookie := signupDevice(t, h)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, prefix := range []string{"race1", "race2"} {
		email := uniqueEmail(t, prefix)
		wg.Add(1)
		go func() { defer wg.Done(); results <- riskSignup(t, h, email, offer.Code, cookie, "198.51.100.30") }()
	}
	wg.Wait()
	close(results)
	allowed := 0
	review := 0
	for response := range results {
		if response.Code != 202 {
			t.Fatalf("signup: %s", response.Body.String())
		}
		var p RegistrationPendingResponse
		_ = json.Unmarshal(response.Body.Bytes(), &p)
		if p.RewardReviewRequired {
			review++
		} else {
			allowed++
		}
	}
	if allowed != 1 || review != 1 {
		t.Fatalf("concurrent decisions allow=%d review=%d", allowed, review)
	}
	var raw string
	if err := db.QueryRowContext(t.Context(), `SELECT network_hash FROM signup_risk_profiles WHERE user_id IN(SELECT user_id FROM promotion_registrations WHERE invite_id=$1) LIMIT 1`, offer.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 64 || strings.Contains(raw, "198.51") {
		t.Fatal("raw IP stored")
	}
}

func TestSignupRiskNetworkDailyCapsAndObserveMode(t *testing.T) {
	h, _, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 10)
	service := setupSignupRisk(t, h)
	var actor string
	_ = db.QueryRowContext(t.Context(), `SELECT created_by FROM promotion_invites WHERE id=$1`, offer.ID).Scan(&actor)
	before, err := service.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.UpdateSettings(context.Background(), before, actor) })
	settings := risk.Settings{Enabled: true, DeviceLimit: 5, NetworkDailyLimit: 1, AutomaticDailyLimit: 100}
	if err := service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	first := riskSignup(t, h, uniqueEmail(t, "network1"), offer.Code, signupDevice(t, h), "198.51.100.40")
	if first.Code != 202 || !strings.Contains(first.Body.String(), `"reward_review_required":false`) {
		t.Fatalf("first %s", first.Body.String())
	}
	secondEmail := uniqueEmail(t, "network2")
	second := riskSignup(t, h, secondEmail, offer.Code, signupDevice(t, h), "198.51.100.40")
	if second.Code != 202 || !strings.Contains(second.Body.String(), `"reward_review_required":true`) {
		t.Fatalf("network cap %s", second.Body.String())
	}
	profiles, _, err := service.List(t.Context(), "review", secondEmail, 20, 0)
	if err != nil || len(profiles) != 1 {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(profiles[0].Reasons, ","), "network_velocity") {
		t.Fatalf("network reason %+v", profiles[0])
	}
	settings.AutomaticDailyLimit = 1
	if err := service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	thirdEmail := uniqueEmail(t, "daily")
	third := riskSignup(t, h, thirdEmail, offer.Code, signupDevice(t, h), "198.51.100.41")
	if third.Code != 202 {
		t.Fatalf("daily signup %s", third.Body.String())
	}
	profiles, _, err = service.List(t.Context(), "review", thirdEmail, 20, 0)
	if err != nil || len(profiles) != 1 || !strings.Contains(strings.Join(profiles[0].Reasons, ","), "daily_cap") {
		t.Fatalf("daily cap %+v %v", profiles, err)
	}
	settings.Enabled = false
	if err := service.UpdateSettings(t.Context(), settings, actor); err != nil {
		t.Fatal(err)
	}
	fourth := riskSignup(t, h, uniqueEmail(t, "observe"), offer.Code, nil, "198.51.100.40")
	if fourth.Code != 202 || !strings.Contains(fourth.Body.String(), `"reward_review_required":false`) {
		t.Fatalf("observe mode %s", fourth.Body.String())
	}
	profiles, _, _ = service.List(t.Context(), "review", secondEmail, 20, 0)
	if len(profiles) != 1 {
		t.Fatal("disabling rules released existing hold")
	}
}

func TestSignupRiskRetainsEmailHistoryButPrunesNetworkIdentifiers(t *testing.T) {
	h, _, db := verificationIntegrationSetup(t)
	offer := createTestPromotion(t, h, 3)
	service := setupSignupRisk(t, h)
	email := uniqueEmail(t, "history")
	first := riskSignup(t, h, email, offer.Code, signupDevice(t, h), "198.51.100.50")
	if first.Code != 202 {
		t.Fatalf("signup %s", first.Body.String())
	}
	u, _ := h.store.GetUserByEmail(t.Context(), email)
	var emailHash string
	if err := db.QueryRowContext(t.Context(), `UPDATE signup_risk_profiles SET created_at=NOW()-INTERVAL '32 days' WHERE user_id=$1 RETURNING email_hash`, u.ID).Scan(&emailHash); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM signup_risk_profiles WHERE email_hash=$1`, emailHash)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE email=$1`, email)
	})
	if err := service.PruneSignals(t.Context()); err != nil {
		t.Fatal(err)
	}
	var pruned bool
	if err := db.QueryRowContext(t.Context(), `SELECT device_hash IS NULL AND network_hash IS NULL FROM signup_risk_profiles WHERE user_id=$1`, u.ID).Scan(&pruned); err != nil {
		t.Fatal(err)
	}
	if !pruned {
		t.Fatal("old correlation identifiers remain")
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM users WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	// A new non-promotion account still cannot use deletion to get automatic trial credit.
	second := riskSignup(t, h, email, "", signupDevice(t, h), "198.51.100.51")
	if second.Code != 202 || !strings.Contains(second.Body.String(), `"reward_review_required":true`) {
		t.Fatalf("deleted-account replay: %d %s", second.Code, second.Body.String())
	}
	profiles, _, err := service.List(t.Context(), "review", email, 20, 0)
	if err != nil || len(profiles) != 1 || !strings.Contains(strings.Join(profiles[0].Reasons, ","), "previous_email") {
		t.Fatalf("email history: %+v %v", profiles, err)
	}
}
