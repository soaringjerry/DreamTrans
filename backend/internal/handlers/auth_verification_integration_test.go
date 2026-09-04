package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/mailer"
	"github.com/dreamtrans/backend/internal/store"
)

// captureMailer records outbound mail so the test can follow the link.
type captureMailer struct {
	mu       sync.Mutex
	messages []mailer.Message
}

func (c *captureMailer) Send(_ context.Context, message mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, message)
	return nil
}

func (c *captureMailer) last(t *testing.T) mailer.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		t.Fatal("no verification mail was sent")
	}
	return c.messages[len(c.messages)-1]
}

var verifyLinkPattern = regexp.MustCompile(`/pro\?verify=([A-Za-z0-9_-]+)`)

func verificationIntegrationSetup(t *testing.T) (*AuthHandler, *captureMailer, *sql.DB) {
	t.Helper()
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REGISTRATION_ENABLED", "true")
	t.Setenv("REGISTRATION_INVITE_CODE", "")
	t.Setenv("EMAIL_VERIFICATION_REQUIRED", "")
	t.Setenv("APP_BASE_URL", "https://app.example.test")
	pgStore, err := store.NewPostgresStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pgStore.Close() })
	jwtManager, err := auth.NewJWTManagerWithSecrets("verification-test-access-secret-0123456789", "verification-test-refresh-secret-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAuthHandler(pgStore, jwtManager)
	mail := &captureMailer{}
	handler.SetMailer(mail)
	handler.SetRegistrationPolicy(auth.NewRegistrationPolicy(nil, nil))
	return handler, mail, pgStore.DB()
}

func postJSON(t *testing.T, handle http.HandlerFunc, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handle(recorder, request)
	return recorder
}

func uniqueEmail(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + "@example.test"
}

func cleanupUser(t *testing.T, db *sql.DB, email string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE email = $1`, email)
	})
}

func TestRegistrationRequiresEmailVerificationBeforeLogin(t *testing.T) {
	handler, mail, db := verificationIntegrationSetup(t)
	email := uniqueEmail(t, "verify")
	cleanupUser(t, db, email)
	password := "correct horse battery"

	registered := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": email, "password": password, "name": "Verify Me",
	})
	if registered.Code != http.StatusAccepted {
		t.Fatalf("register status = %d, body %s", registered.Code, registered.Body.String())
	}
	var pending RegistrationPendingResponse
	if err := json.Unmarshal(registered.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if !pending.VerificationRequired || !pending.EmailSent || pending.Email != email {
		t.Fatalf("unexpected pending response: %+v", pending)
	}
	if strings.Contains(registered.Body.String(), "access_token") {
		t.Fatal("registration must not hand out tokens before verification")
	}

	// Unverified accounts cannot log in yet.
	login := postJSON(t, handler.HandleLogin, "/api/auth/login", map[string]any{"email": email, "password": password})
	if login.Code != http.StatusForbidden || !strings.Contains(login.Body.String(), "email_not_verified") {
		t.Fatalf("login before verification: status = %d, body %s", login.Code, login.Body.String())
	}

	message := mail.last(t)
	if message.To != email || !strings.Contains(message.Text, "https://app.example.test/pro?verify=") {
		t.Fatalf("unexpected mail: to=%s text=%q", message.To, message.Text)
	}
	match := verifyLinkPattern.FindStringSubmatch(message.Text)
	if len(match) != 2 {
		t.Fatalf("no verification link in mail text: %q", message.Text)
	}
	token := match[1]

	// A tampered token is rejected without leaking why.
	bad := postJSON(t, handler.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": token + "x"})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "verification_token_invalid") {
		t.Fatalf("tampered token: status = %d, body %s", bad.Code, bad.Body.String())
	}

	verified := postJSON(t, handler.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": token})
	if verified.Code != http.StatusOK {
		t.Fatalf("verify status = %d, body %s", verified.Code, verified.Body.String())
	}
	var session AuthResponse
	if err := json.Unmarshal(verified.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.AccessToken == "" || session.User == nil || !session.User.EmailVerified {
		t.Fatalf("verification should sign the user in as verified: %+v", session.User)
	}

	// The link is single use.
	replay := postJSON(t, handler.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": token})
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed token: status = %d", replay.Code)
	}

	// And login now works.
	login = postJSON(t, handler.HandleLogin, "/api/auth/login", map[string]any{"email": email, "password": password})
	if login.Code != http.StatusOK {
		t.Fatalf("login after verification: status = %d, body %s", login.Code, login.Body.String())
	}
}

func TestRegistrationRejectsAliasesOfAnExistingInbox(t *testing.T) {
	handler, _, db := verificationIntegrationSetup(t)
	base := "alias-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	first := base + "@gmail.com"
	alias := strings.ToUpper(base[:1]) + base[1:2] + "." + base[2:] + "+trial@googlemail.com"
	cleanupUser(t, db, first)

	created := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": first, "password": "correct horse battery", "name": "First",
	})
	if created.Code != http.StatusAccepted {
		t.Fatalf("first register status = %d, body %s", created.Code, created.Body.String())
	}
	duplicate := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": alias, "password": "correct horse battery", "name": "Second",
	})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("alias register status = %d, body %s", duplicate.Code, duplicate.Body.String())
	}
}

func TestRegistrationRejectsDisposableDomains(t *testing.T) {
	handler, mail, _ := verificationIntegrationSetup(t)
	rejected := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": "burner@mailinator.com", "password": "correct horse battery", "name": "Burner",
	})
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("disposable register status = %d, body %s", rejected.Code, rejected.Body.String())
	}
	if len(mail.messages) != 0 {
		t.Fatal("no mail should be sent for a rejected sign-up")
	}
}

func TestResendVerificationNeverRevealsAccounts(t *testing.T) {
	handler, mail, db := verificationIntegrationSetup(t)
	email := uniqueEmail(t, "resend")
	cleanupUser(t, db, email)

	unknown := postJSON(t, handler.HandleResendVerification, "/api/auth/resend-verification", map[string]any{"email": "nobody-" + email})
	if unknown.Code != http.StatusOK {
		t.Fatalf("unknown address: status = %d, body %s", unknown.Code, unknown.Body.String())
	}
	if len(mail.messages) != 0 {
		t.Fatal("unknown address must not trigger mail")
	}

	postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": email, "password": "correct horse battery", "name": "Resend",
	})
	sentAfterRegister := len(mail.messages)
	// Immediately asking again is accepted but throttled: the first link is
	// still valid, so no second mail goes out.
	again := postJSON(t, handler.HandleResendVerification, "/api/auth/resend-verification", map[string]any{"email": email})
	if again.Code != http.StatusOK {
		t.Fatalf("resend status = %d, body %s", again.Code, again.Body.String())
	}
	if len(mail.messages) != sentAfterRegister {
		t.Fatalf("resend inside the cooldown sent mail: %d -> %d", sentAfterRegister, len(mail.messages))
	}
	// Age the previous token past the cooldown; the next resend must send.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE email_verification_tokens SET created_at = NOW() - INTERVAL '2 minutes'
		WHERE user_id = (SELECT id FROM users WHERE email = $1)
	`, email); err != nil {
		t.Fatal(err)
	}
	postJSON(t, handler.HandleResendVerification, "/api/auth/resend-verification", map[string]any{"email": email})
	if len(mail.messages) != sentAfterRegister+1 {
		t.Fatalf("resend after cooldown did not send: %d", len(mail.messages))
	}
	// Only the newest link works.
	old := verifyLinkPattern.FindStringSubmatch(mail.messages[sentAfterRegister-1].Text)[1]
	stale := postJSON(t, handler.HandleVerifyEmail, "/api/auth/verify-email", map[string]any{"token": old})
	if stale.Code != http.StatusBadRequest {
		t.Fatalf("superseded token accepted: status = %d", stale.Code)
	}
}

func TestRegistrationWithoutMailerIsRefusedByDefault(t *testing.T) {
	handler, _, db := verificationIntegrationSetup(t)
	handler.SetMailer(nil)
	email := uniqueEmail(t, "nomail")
	cleanupUser(t, db, email)
	refused := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": email, "password": "correct horse battery", "name": "No Mail",
	})
	if refused.Code != http.StatusServiceUnavailable || !strings.Contains(refused.Body.String(), "email_delivery_unavailable") {
		t.Fatalf("register without mail transport: status = %d, body %s", refused.Code, refused.Body.String())
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM users WHERE email = $1`, email).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("a refused sign-up must not leave an account behind")
	}
}

func TestRegistrationCanOptOutOfVerificationExplicitly(t *testing.T) {
	handler, _, db := verificationIntegrationSetup(t)
	handler.SetMailer(nil)
	t.Setenv("EMAIL_VERIFICATION_REQUIRED", "false")
	email := uniqueEmail(t, "optout")
	cleanupUser(t, db, email)
	created := postJSON(t, handler.HandleRegister, "/api/auth/register", map[string]any{
		"email": email, "password": "correct horse battery", "name": "Opt Out",
	})
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), "access_token") {
		t.Fatalf("opt-out register status = %d, body %s", created.Code, created.Body.String())
	}
}
