// Package mailer delivers transactional email (verification links) over SMTP.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Message is one outbound email. Text is required; HTML is optional and sent
// as a multipart alternative when present.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Config is read from SMTP_* environment variables.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS is "starttls" (default, port 587), "tls" (implicit, port 465) or
	// "none" (plain text, local relays only).
	TLS     string
	Timeout time.Duration
}

// FromEnv builds a sender from the environment, in this order:
//
//   - RESEND_API_KEY set: the Resend HTTP API (sender address from MAIL_FROM)
//   - SMTP_HOST set: an SMTP relay (SMTP_HOST=log only prints the message,
//     handy on a laptop without a relay)
//   - neither: (nil, false) and self-registration skips verification
func FromEnv() (Sender, bool, error) {
	from := strings.TrimSpace(os.Getenv("MAIL_FROM"))
	if from == "" {
		from = strings.TrimSpace(os.Getenv("SMTP_FROM"))
	}
	if apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY")); apiKey != "" {
		sender, err := NewResendSender(apiKey, from)
		if err != nil {
			return nil, true, err
		}
		return sender, true, nil
	}
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		return nil, false, nil
	}
	if strings.EqualFold(host, "log") {
		return LogSender{}, true, nil
	}
	cfg := Config{
		Host:     host,
		Username: strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     from,
		TLS:      strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS"))),
		Timeout:  15 * time.Second,
	}
	if port := strings.TrimSpace(os.Getenv("SMTP_PORT")); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value <= 0 || value > 65535 {
			return nil, true, fmt.Errorf("SMTP_PORT must be a port number, got %q", port)
		}
		cfg.Port = value
	}
	sender, err := NewSMTPSender(&cfg)
	if err != nil {
		return nil, true, err
	}
	return sender, true, nil
}

// SMTPSender sends through one SMTP relay.
type SMTPSender struct {
	cfg  Config
	from *mail.Address
}

// NewSMTPSender validates the configuration and returns a sender.
func NewSMTPSender(cfg *Config) (*SMTPSender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("SMTP_HOST is required")
	}
	if cfg.From == "" {
		return nil, errors.New("MAIL_FROM is required with SMTP_HOST")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("MAIL_FROM is not a valid address: %w", err)
	}
	switch cfg.TLS {
	case "":
		cfg.TLS = "starttls"
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("SMTP_TLS must be starttls, tls or none, got %q", cfg.TLS)
	}
	if cfg.Port == 0 {
		if cfg.TLS == "tls" {
			cfg.Port = 465
		} else {
			cfg.Port = 587
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &SMTPSender{cfg: *cfg, from: from}, nil
}

// Send delivers one message, honoring the context deadline for the dial.
func (s *SMTPSender) Send(ctx context.Context, message Message) error {
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	var conn net.Conn
	if s.cfg.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if s.cfg.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp relay does not offer STARTTLS; set SMTP_TLS=tls or none")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp relay does not offer AUTH")
		}
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(BuildMIME(s.from, to, message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return client.Quit()
}

// BuildMIME renders the RFC 5322 message. Exported for tests.
func BuildMIME(from, to *mail.Address, message Message) []byte {
	var b strings.Builder
	boundary := "dt-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	write := func(key, value string) {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(sanitizeHeader(value))
		b.WriteString("\r\n")
	}
	write("From", from.String())
	write("To", to.String())
	write("Subject", mimeEncodeHeader(message.Subject))
	write("Date", time.Now().UTC().Format(time.RFC1123Z))
	write("MIME-Version", "1.0")
	write("Auto-Submitted", "auto-generated")
	text := normalizeNewlines(message.Text)
	if message.HTML == "" {
		write("Content-Type", "text/plain; charset=UTF-8")
		write("Content-Transfer-Encoding", "8bit")
		b.WriteString("\r\n")
		b.WriteString(text)
		return []byte(b.String())
	}
	write("Content-Type", "multipart/alternative; boundary="+boundary)
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(text)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normalizeNewlines(message.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String())
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

// sanitizeHeader strips CR/LF so user-influenced values can never inject
// extra headers.
func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

func mimeEncodeHeader(value string) string {
	ascii := true
	for _, r := range value {
		if r > 0x7e || r < 0x20 {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	return encodeRFC2047(value)
}

func encodeRFC2047(value string) string {
	const chunk = 40
	runes := []rune(value)
	var parts []string
	for start := 0; start < len(runes); start += chunk {
		end := start + chunk
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, "=?UTF-8?B?"+base64Std(string(runes[start:end]))+"?=")
	}
	return strings.Join(parts, " ")
}

// LogSender prints the message instead of delivering it (SMTP_HOST=log).
type LogSender struct{}

// Send logs the message body so a developer can copy the verification link.
func (LogSender) Send(_ context.Context, message Message) error {
	log.Printf("mailer(log): to=%s subject=%q\n%s", message.To, message.Subject, message.Text)
	return nil
}
