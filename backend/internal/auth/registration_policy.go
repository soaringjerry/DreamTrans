package auth

import (
	"errors"
	"os"
	"strings"
)

// Errors returned by RegistrationPolicy.Check. Handlers map them to user
// facing messages; the wording here is deliberately generic.
var (
	ErrEmailDomainBlocked    = errors.New("email domain is not accepted")
	ErrEmailDomainNotAllowed = errors.New("email domain is not on the allow list")
)

// disposableEmailDomains are throwaway providers commonly used to farm trial
// credit. Operators extend the list with REGISTRATION_BLOCKED_EMAIL_DOMAINS.
var disposableEmailDomains = []string{
	"10minutemail.com", "10minutemail.net", "20minutemail.com", "33mail.com",
	"anonbox.net", "boun.cr", "burnermail.io", "byom.de", "dispostable.com",
	"discard.email", "emailondeck.com", "fakeinbox.com", "getairmail.com",
	"getnada.com", "guerrillamail.com", "guerrillamail.net", "guerrillamail.org",
	"guerrillamailblock.com", "harakirimail.com", "inboxbear.com", "jetable.org",
	"mail-temp.com", "mail.tm", "mailcatch.com", "maildrop.cc", "mailinator.com",
	"mailnesia.com", "mailnull.com", "mailsac.com", "mintemail.com", "moakt.com",
	"mohmal.com", "mytemp.email", "nowmymail.com", "sharklasers.com",
	"spam4.me", "spamgourmet.com", "temp-mail.io", "temp-mail.org", "tempail.com",
	"tempmail.com", "tempmail.dev", "tempmailo.com", "tempr.email", "throwam.com",
	"throwawaymail.com", "trashmail.com", "trashmail.me", "yopmail.com",
	"yopmail.fr", "zetmail.com",
}

// RegistrationPolicy decides which email addresses may self-register.
type RegistrationPolicy struct {
	allowed map[string]struct{}
	blocked map[string]struct{}
}

// RegistrationPolicyFromEnv reads REGISTRATION_ALLOWED_EMAIL_DOMAINS (when set,
// only these domains may register) and REGISTRATION_BLOCKED_EMAIL_DOMAINS
// (added to the built-in disposable list). Both are comma separated.
func RegistrationPolicyFromEnv() *RegistrationPolicy {
	return NewRegistrationPolicy(
		splitDomains(os.Getenv("REGISTRATION_ALLOWED_EMAIL_DOMAINS")),
		splitDomains(os.Getenv("REGISTRATION_BLOCKED_EMAIL_DOMAINS")),
	)
}

// NewRegistrationPolicy builds a policy from explicit lists.
func NewRegistrationPolicy(allowed, blocked []string) *RegistrationPolicy {
	policy := &RegistrationPolicy{
		allowed: make(map[string]struct{}, len(allowed)),
		blocked: make(map[string]struct{}, len(blocked)+len(disposableEmailDomains)),
	}
	for _, domain := range allowed {
		policy.allowed[normalizeDomain(domain)] = struct{}{}
	}
	for _, domain := range disposableEmailDomains {
		policy.blocked[domain] = struct{}{}
	}
	for _, domain := range blocked {
		policy.blocked[normalizeDomain(domain)] = struct{}{}
	}
	return policy
}

// Check returns nil when the (already lower-cased, validated) address may
// register. The allow list wins over the block list so an operator can whitelist
// a domain the built-in list rejects.
func (p *RegistrationPolicy) Check(email string) error {
	if p == nil {
		return nil
	}
	domain := normalizeDomain(emailDomain(email))
	if len(p.allowed) > 0 {
		if _, ok := p.allowed[domain]; ok {
			return nil
		}
		// Subdomains of an allowed domain (e.g. student.monash.edu) count.
		for allowed := range p.allowed {
			if strings.HasSuffix(domain, "."+allowed) {
				return nil
			}
		}
		return ErrEmailDomainNotAllowed
	}
	if _, ok := p.blocked[domain]; ok {
		return ErrEmailDomainBlocked
	}
	return nil
}

// CanonicalEmail folds aliases of one inbox onto a single key: lower-case,
// "+tag" removed, and for Gmail the dots in the local part removed with the
// googlemail.com alias mapped to gmail.com. It is used to stop one person from
// collecting trial credit many times, never for delivery.
func CanonicalEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return email
	}
	local, domain := email[:at], email[at+1:]
	if plus := strings.Index(local, "+"); plus >= 0 {
		local = local[:plus]
	}
	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}
	return local + "@" + domain
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}

func normalizeDomain(domain string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "@")
}

func splitDomains(value string) []string {
	var domains []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			domains = append(domains, trimmed)
		}
	}
	return domains
}
