package mailer

import (
	"strings"
	"testing"
)

func TestFromEnvPrefersResendOverSMTP(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_live_example_key")
	t.Setenv("MAIL_FROM", "DreamTrans <no-reply@example.com>")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	sender, configured, err := FromEnv()
	if err != nil || !configured {
		t.Fatalf("FromEnv() = %v, %v, %v", sender, configured, err)
	}
	if _, ok := sender.(*ResendSender); !ok {
		t.Fatalf("expected ResendSender, got %T", sender)
	}
}

func TestFromEnvRejectsPlaceholderResendKey(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_xxxxxxxxx")
	t.Setenv("MAIL_FROM", "no-reply@example.com")
	t.Setenv("SMTP_HOST", "")
	if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder key accepted: err = %v", err)
	}
}

func TestFromEnvResendNeedsSenderAddress(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_live_example_key")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("SMTP_FROM", "")
	if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "MAIL_FROM") {
		t.Fatalf("missing MAIL_FROM accepted: err = %v", err)
	}
}

func TestFromEnvFallsBackToSMTPAndLegacyFromVariable(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("SMTP_FROM", "no-reply@example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SMTP_TLS", "")
	sender, configured, err := FromEnv()
	if err != nil || !configured {
		t.Fatalf("FromEnv() = %v, %v, %v", sender, configured, err)
	}
	smtpSender, ok := sender.(*SMTPSender)
	if !ok {
		t.Fatalf("expected SMTPSender, got %T", sender)
	}
	if smtpSender.cfg.Port != 587 || smtpSender.cfg.TLS != "starttls" {
		t.Fatalf("unexpected SMTP defaults: %+v", smtpSender.cfg)
	}
}

func TestFromEnvUnconfigured(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_HOST", "")
	sender, configured, err := FromEnv()
	if sender != nil || configured || err != nil {
		t.Fatalf("FromEnv() = %v, %v, %v; want nil, false, nil", sender, configured, err)
	}
}
