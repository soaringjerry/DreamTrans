package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/mailer"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

const (
	verificationTokenTTL      = 24 * time.Hour
	verificationResendMinimum = 60 * time.Second
	verificationTokenBytes    = 32
)

// SetMailer installs the transactional mail sender. Verification is required
// for self-registration whenever a sender is present unless
// EMAIL_VERIFICATION_REQUIRED=false.
func (h *AuthHandler) SetMailer(sender mailer.Sender) {
	h.mailer = sender
}

// SetRegistrationPolicy installs the email domain policy for self-registration.
func (h *AuthHandler) SetRegistrationPolicy(policy *auth.RegistrationPolicy) {
	h.policy = policy
}

// SetAppName overrides the product name used in outbound mail.
func (h *AuthHandler) SetAppName(name string) {
	if strings.TrimSpace(name) != "" {
		h.appName = strings.TrimSpace(name)
	}
}

// EmailVerificationRequired reports whether new sign-ups must confirm their
// address before they can log in.
func (h *AuthHandler) EmailVerificationRequired() bool {
	return EmailVerificationRequiredFromEnv(h.mailer != nil)
}

// EmailVerificationRequiredFromEnv resolves EMAIL_VERIFICATION_REQUIRED.
// Verification is on unless the operator explicitly sets it to false: a
// production install that forgot to configure mail must refuse sign-ups
// rather than silently hand out unverified accounts and trial credit.
func EmailVerificationRequiredFromEnv(_ bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EMAIL_VERIFICATION_REQUIRED"))) {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}

func (h *AuthHandler) productName() string {
	if h.appName != "" {
		return h.appName
	}
	return "Yufolo"
}

// registrationPolicyError maps policy failures to client messages (all 403).
func registrationPolicyError(err error) string {
	switch {
	case errors.Is(err, auth.ErrEmailDomainNotAllowed):
		return "email domain is not allowed to register"
	case errors.Is(err, auth.ErrEmailDomainBlocked):
		return "disposable email addresses are not accepted"
	default:
		return "email address is not accepted"
	}
}

func newVerificationToken() (raw, hash string, err error) {
	buf := make([]byte, verificationTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashVerificationToken(raw), nil
}

func hashVerificationToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// issueVerificationEmail creates a token and sends the link. The store write
// happens first so a delivery failure never leaves a link that cannot be
// redeemed; a failed send is reported so the client can offer a resend.
func (h *AuthHandler) issueVerificationEmail(ctx context.Context, r *http.Request, user *models.User) error {
	base, err := verificationBaseURL()
	if err != nil {
		return err
	}
	raw, hash, err := newVerificationToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	if err := h.store.CreateEmailVerificationToken(ctx, user.ID, hash, time.Now().Add(verificationTokenTTL)); err != nil {
		return fmt.Errorf("store token: %w", err)
	}
	link := base + "/pro?verify=" + raw
	message := verificationMessage(h.productName(), user, link)
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := h.mailer.Send(sendCtx, message); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}

// Verification links carry login credentials and must never trust request headers.
func verificationBaseURL() (string, error) {
	raw := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" ||
		(u.Scheme != "https" && u.Scheme != "http") || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\"'<>\\") {
		return "", errors.New("APP_BASE_URL must be an explicit HTTP(S) origin for verification email")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func verificationMessage(product string, user *models.User, link string) mailer.Message {
	name := strings.TrimSpace(user.Name)
	if name == "" {
		name = user.Email
	}
	hours := int(verificationTokenTTL.Hours())
	text := fmt.Sprintf(`%s，你好：

感谢注册 %s。请点击下面的链接验证邮箱并激活账户（%d 小时内有效）：

%s

如果这不是你的操作，忽略这封邮件即可，账户不会被激活。

— %s
`, name, product, hours, link, product)
	html := fmt.Sprintf(`<!doctype html>
<html><body style="margin:0;padding:24px;background:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'PingFang SC','Microsoft YaHei',sans-serif;color:#172033">
<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:14px;padding:32px;border:1px solid #e5e7eb">
<p style="margin:0 0 16px;font-size:15px">%s，你好：</p>
<p style="margin:0 0 20px;font-size:15px;line-height:1.6">感谢注册 %s。请点击下面的按钮验证邮箱并激活账户，链接 %d 小时内有效。</p>
<p style="margin:0 0 24px"><a href="%s" style="display:inline-block;padding:12px 22px;background:#3159d8;color:#ffffff;text-decoration:none;border-radius:10px;font-weight:600">验证邮箱</a></p>
<p style="margin:0 0 8px;font-size:13px;color:#697386">按钮无法点击时，复制这个地址到浏览器打开：</p>
<p style="margin:0 0 24px;font-size:13px;word-break:break-all"><a href="%s" style="color:#3159d8">%s</a></p>
<p style="margin:0;font-size:13px;color:#697386;line-height:1.6">如果这不是你的操作，忽略这封邮件即可，账户不会被激活。</p>
</div></body></html>
`, htmlEscape(name), htmlEscape(product), hours, htmlEscape(link), htmlEscape(link), htmlEscape(link))
	return mailer.Message{
		To:      user.Email,
		Subject: fmt.Sprintf("验证你的 %s 邮箱", product),
		Text:    text,
		HTML:    html,
	}
}

func htmlEscape(value string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
	).Replace(value)
}

// VerifyEmailRequest carries the token from the emailed link.
type VerifyEmailRequest struct {
	Token string `json:"token"`
}

// HandleVerifyEmail redeems a verification link, grants the trial credit and
// signs the user in.
func (h *AuthHandler) HandleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req VerifyEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || len(req.Token) > 128 {
		http.Error(w, `{"error":"verification link is invalid or has expired","code":"verification_token_invalid"}`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	user, err := h.store.ConsumeEmailVerificationToken(ctx, hashVerificationToken(req.Token))
	if errors.Is(err, store.ErrVerificationTokenInvalid) {
		http.Error(w, `{"error":"verification link is invalid or has expired","code":"verification_token_invalid"}`, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if !user.IsActive {
		http.Error(w, `{"error":"account disabled"}`, http.StatusForbidden)
		return
	}
	if h.billing != nil {
		if err := h.billing.GrantTrialCredit(ctx, user.ID); err != nil {
			log.Printf("grant trial credit for %s: %v", user.ID, err)
		}
	}
	h.respondWithSession(ctx, w, user)
}

// ResendVerificationRequest asks for a fresh link.
type ResendVerificationRequest struct {
	Email string `json:"email"`
}

// HandleResendVerification issues a new link for an unverified account. The
// response is identical whether or not the address exists so the endpoint
// cannot be used to enumerate accounts.
func (h *AuthHandler) HandleResendVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if h.mailer == nil {
		http.Error(w, `{"error":"email delivery is not configured","code":"email_delivery_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if _, err := verificationBaseURL(); err != nil {
		http.Error(w, `{"error":"verification email address is not configured","code":"email_delivery_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	var req ResendVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(req.Email); req.Email == "" || err != nil {
		http.Error(w, `{"error":"invalid email"}`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	accepted := func() {
		w.Header().Set("Content-Type", "application/json")
		encodeJSONResponse(w, map[string]any{"accepted": true})
	}
	user, err := h.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if user == nil || user.EmailVerified || !user.IsActive {
		accepted()
		return
	}
	issued, err := h.store.LatestEmailVerificationIssuedAt(ctx, user.ID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if !issued.IsZero() && time.Since(issued) < verificationResendMinimum {
		// Quietly accept: the previous link is still valid and a burst of
		// resends must not turn into a burst of outbound mail.
		accepted()
		return
	}
	if err := h.issueVerificationEmail(ctx, r, user); err != nil {
		log.Printf("resend verification for %s: %v", user.ID, err)
		http.Error(w, `{"error":"failed to send verification email","code":"email_delivery_failed"}`, http.StatusBadGateway)
		return
	}
	accepted()
}

// respondWithSession issues tokens for an authenticated user (shared by login,
// legacy registration and email verification).
func (h *AuthHandler) respondWithSession(ctx context.Context, w http.ResponseWriter, user *models.User) {
	accessToken, err := h.jwtManager.GenerateAccessToken(user.ID, user.TenantID, user.Email, user.Role)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}
	refreshToken, refreshHash, refreshExpiry, err := h.jwtManager.GenerateRefreshToken(user.ID)
	if err != nil {
		http.Error(w, `{"error":"failed to generate refresh token"}`, http.StatusInternalServerError)
		return
	}
	if err := h.store.CreateRefreshToken(ctx, &models.RefreshToken{
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: refreshExpiry,
	}); err != nil {
		http.Error(w, `{"error":"failed to store refresh token"}`, http.StatusInternalServerError)
		return
	}
	if err := h.store.UpdateUserLastLogin(ctx, user.ID); err != nil {
		log.Printf("failed to update user last login: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, AuthResponse{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(h.jwtManager.AccessTokenExpiry().Seconds()),
	})
}
