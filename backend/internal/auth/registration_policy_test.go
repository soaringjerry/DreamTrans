package auth

import (
	"errors"
	"testing"
)

func TestCanonicalEmailFoldsAliases(t *testing.T) {
	cases := map[string]string{
		"Jane.Doe+promo@Gmail.com":  "janedoe@gmail.com",
		"jane.doe@googlemail.com":   "janedoe@gmail.com",
		"first.last+tag@monash.edu": "first.last@monash.edu",
		"PLAIN@EXAMPLE.ORG":         "plain@example.org",
		"no-at-sign":                "no-at-sign",
	}
	for input, want := range cases {
		if got := CanonicalEmail(input); got != want {
			t.Errorf("CanonicalEmail(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegistrationPolicyBlocksDisposableDomains(t *testing.T) {
	policy := NewRegistrationPolicy(nil, []string{"Corp-Spam.example"})
	if err := policy.Check("someone@mailinator.com"); !errors.Is(err, ErrEmailDomainBlocked) {
		t.Fatalf("built-in disposable domain: err = %v", err)
	}
	if err := policy.Check("someone@corp-spam.example"); !errors.Is(err, ErrEmailDomainBlocked) {
		t.Fatalf("operator blocked domain: err = %v", err)
	}
	if err := policy.Check("someone@example.com"); err != nil {
		t.Fatalf("ordinary domain: err = %v", err)
	}
}

func TestRegistrationPolicyAllowListWinsAndCoversSubdomains(t *testing.T) {
	policy := NewRegistrationPolicy([]string{"monash.edu", "mailinator.com"}, nil)
	if err := policy.Check("s@student.monash.edu"); err != nil {
		t.Fatalf("subdomain of allowed domain: err = %v", err)
	}
	if err := policy.Check("s@mailinator.com"); err != nil {
		t.Fatalf("explicitly allowed domain must beat the block list: err = %v", err)
	}
	if err := policy.Check("s@gmail.com"); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Fatalf("domain outside allow list: err = %v", err)
	}
	if err := policy.Check("s@notmonash.edu"); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Fatalf("suffix without a dot boundary must not match: err = %v", err)
	}
}

func TestNilPolicyAcceptsEverything(t *testing.T) {
	var policy *RegistrationPolicy
	if err := policy.Check("anyone@mailinator.com"); err != nil {
		t.Fatalf("nil policy: err = %v", err)
	}
}
