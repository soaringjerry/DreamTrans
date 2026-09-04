package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	resend "github.com/resend/resend-go/v4"
)

// ResendSender delivers through the Resend HTTP API (https://resend.com).
// Selected by RESEND_API_KEY; the sender address comes from MAIL_FROM.
type ResendSender struct {
	client *resend.Client
	from   string
}

// NewResendSender validates the key and sender address.
func NewResendSender(apiKey, from string) (*ResendSender, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("RESEND_API_KEY is required")
	}
	if strings.HasPrefix(apiKey, "re_xxx") {
		return nil, errors.New("RESEND_API_KEY still holds the placeholder value; paste the key from the Resend dashboard")
	}
	from = strings.TrimSpace(from)
	if from == "" {
		return nil, errors.New("MAIL_FROM is required with RESEND_API_KEY (for example \"DreamTrans <no-reply@yourdomain.com>\")")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("MAIL_FROM is not a valid address: %w", err)
	}
	return &ResendSender{client: resend.NewClient(apiKey), from: from}, nil
}

// Send delivers one message. Resend enforces its own rate limits; a failure
// surfaces to the caller so the sign-up flow can offer a resend.
func (s *ResendSender) Send(ctx context.Context, message Message) error {
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	params := &resend.SendEmailRequest{
		From:    s.from,
		To:      []string{to.Address},
		Subject: sanitizeHeader(message.Subject),
		Text:    message.Text,
		Html:    message.HTML,
		Headers: map[string]string{"Auto-Submitted": "auto-generated"},
	}
	if _, err := s.client.Emails.SendWithContext(ctx, params); err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	return nil
}
