package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalAuth "github.com/dreamtrans/backend/internal/auth"
)

func TestLiveStreamRegistryEnforcesLimitAtomically(t *testing.T) {
	registry := newLiveTranscriptionRegistry()

	releaseFirst, err := registry.Acquire(
		&liveTranscriptionStream{ConnectionID: "conn-1", UserID: "user-a"}, 1,
	)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := registry.Acquire(
		&liveTranscriptionStream{ConnectionID: "conn-2", UserID: "user-a"}, 1,
	); !errors.Is(err, ErrConcurrentStreamLimit) {
		t.Fatalf("second acquire error = %v, want ErrConcurrentStreamLimit", err)
	}

	// Another user is not affected, and -1 means unlimited.
	releaseOther, err := registry.Acquire(
		&liveTranscriptionStream{ConnectionID: "conn-3", UserID: "user-b"}, 1,
	)
	if err != nil {
		t.Fatalf("other user acquire failed: %v", err)
	}
	releaseOther()
	if _, err := registry.Acquire(
		&liveTranscriptionStream{ConnectionID: "conn-4", UserID: "user-a"}, -1,
	); err != nil {
		t.Fatalf("unlimited acquire failed: %v", err)
	}

	// Releasing frees the slot; releasing twice must not double-free.
	releaseFirst()
	releaseFirst()
	if got := registry.CountByUser("user-a"); got != 1 {
		t.Fatalf("CountByUser after release = %d, want 1 (the unlimited stream)", got)
	}
}

func TestLiveStreamRegistryTerminateRespectsOwnership(t *testing.T) {
	registry := newLiveTranscriptionRegistry()
	release, err := registry.Acquire(&liveTranscriptionStream{
		ConnectionID: "conn-1", UserID: "user-a", SessionID: "session-1",
	}, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var gotReason string
	registry.SetTerminate("conn-1", func(reason string) { gotReason = reason })

	if registry.Terminate("conn-1", "user-b", "nope", false) {
		t.Fatal("another user terminated a stream they do not own")
	}
	if gotReason != "" {
		t.Fatalf("terminate fired for a rejected request: %q", gotReason)
	}
	if !registry.Terminate("conn-1", "user-a", "owner ended it", false) {
		t.Fatal("owner could not terminate their own stream")
	}
	if gotReason != "owner ended it" {
		t.Fatalf("terminate reason = %q, want %q", gotReason, "owner ended it")
	}

	// Admin bypasses ownership.
	gotReason = ""
	if !registry.Terminate("conn-1", "", "admin cut", true) {
		t.Fatal("admin could not terminate the stream")
	}
	if gotReason != "admin cut" {
		t.Fatalf("admin terminate reason = %q", gotReason)
	}
}

func TestLiveStreamRegistryTerminateBySessionAndPendingReason(t *testing.T) {
	registry := newLiveTranscriptionRegistry()
	release, err := registry.Acquire(&liveTranscriptionStream{
		ConnectionID: "conn-1", UserID: "user-a", SessionID: "session-1",
	}, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Termination that lands before the upgrade installs the callback is
	// remembered and applied when SetTerminate arrives.
	if got := registry.TerminateBySession("user-a", "session-1", "ended elsewhere"); got != 1 {
		t.Fatalf("TerminateBySession = %d, want 1", got)
	}
	var gotReason string
	registry.SetTerminate("conn-1", func(reason string) { gotReason = reason })
	if gotReason != "ended elsewhere" {
		t.Fatalf("pending reason not applied: %q", gotReason)
	}

	// Session and user must both match.
	if got := registry.TerminateBySession("user-b", "session-1", "x"); got != 0 {
		t.Fatalf("cross-user TerminateBySession = %d, want 0", got)
	}
	if got := registry.TerminateBySession("user-a", "session-2", "x"); got != 0 {
		t.Fatalf("wrong-session TerminateBySession = %d, want 0", got)
	}
}

func TestSpeechmaticsPreflightReportsConcurrentStreamLimit(t *testing.T) {
	registry := newLiveTranscriptionRegistry()
	release, err := registry.Acquire(
		&liveTranscriptionStream{ConnectionID: "conn-1", UserID: "user-1"}, -1,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	one := 1
	handler := &SpeechmaticsProxyHandler{
		billing:     &speechmaticsBillingStub{streamLimit: &one},
		liveStreams: registry,
	}
	request := httptest.NewRequest(http.MethodGet, "/api/speechmatics/preflight", nil)
	request = request.WithContext(context.WithValue(
		request.Context(),
		internalAuth.UserClaimsKey,
		&internalAuth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
	response := httptest.NewRecorder()
	handler.HandlePreflight(response, request)
	if response.Code != http.StatusPaymentRequired ||
		!strings.Contains(response.Body.String(), speechmaticsConcurrentLimitMessage) {
		t.Fatalf(
			"preflight over the stream limit: status=%d body=%q",
			response.Code, response.Body.String(),
		)
	}

	// Freeing the slot clears the failure.
	release()
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/speechmatics/preflight", nil)
	request = request.WithContext(context.WithValue(
		request.Context(),
		internalAuth.UserClaimsKey,
		&internalAuth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
	handler.HandlePreflight(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"preflight after release: status=%d body=%q",
			response.Code, response.Body.String(),
		)
	}
}
