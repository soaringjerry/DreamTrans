package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

// Begin in report-only mode to observe deployment-specific resource origins.
// Inline style attributes are used for feed layout and progress indicators;
// scripts do not need unsafe-inline or unsafe-eval. Workers and saved audio
// use blob URLs. Provider traffic goes through the same-origin backend.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; worker-src 'self' blob:; media-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; report-uri /api/security/csp-report"

func securityHeaders(next http.Handler) http.Handler {
	header := "Content-Security-Policy-Report-Only"
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CSP_MODE")), "enforce") {
		header = "Content-Security-Policy"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(header, contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func handleCSPReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var report struct {
		Body struct {
			Directive string `json:"effective-directive"`
		} `json:"csp-report"`
	}
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Reports are unauthenticated diagnostics, not trusted security events.
	// Never log URLs, query strings, script samples or arbitrary report text:
	// those can contain verification tokens, private content or forged logs.
	switch report.Body.Directive {
	case "script-src", "script-src-elem", "script-src-attr", "style-src", "style-src-elem", "style-src-attr",
		"img-src", "font-src", "connect-src", "worker-src", "media-src", "object-src", "base-uri", "form-action", "frame-ancestors":
		log.Printf("CSP browser report: directive=%s", report.Body.Directive)
	}
	w.WriteHeader(http.StatusNoContent)
}
