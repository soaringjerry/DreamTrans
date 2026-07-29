package handlers

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestWebSocketConnectionLimiterEnforcesTotalAndPrincipalLimits(t *testing.T) {
	limiter := newWebSocketConnectionLimiter(2, 1)

	releaseUserOne, ok := limiter.acquire("user:tenant:user-1")
	if !ok {
		t.Fatal("first user slot was rejected")
	}
	defer releaseUserOne()
	if release, acquired := limiter.acquire("user:tenant:user-1"); acquired {
		release()
		t.Fatal("per-principal limit was bypassed")
	}

	releaseUserTwo, ok := limiter.acquire("user:tenant:user-2")
	if !ok {
		t.Fatal("second total slot was rejected")
	}
	if release, acquired := limiter.acquire("user:tenant:user-3"); acquired {
		release()
		t.Fatal("global connection limit was bypassed")
	}

	releaseUserTwo()
	releaseUserTwo() // Release callbacks must be safe in competing cleanup paths.
	releaseUserThree, ok := limiter.acquire("user:tenant:user-3")
	if !ok {
		t.Fatal("released global slot was not reusable")
	}
	releaseUserThree()
}

func TestWebSocketConnectionLimiterIsSafeUnderConcurrentAcquisition(t *testing.T) {
	const attempts = 32
	limiter := newWebSocketConnectionLimiter(8, 3)
	start := make(chan struct{})
	releaseHeld := make(chan struct{})
	results := make(chan bool, attempts)
	var waitGroup sync.WaitGroup

	for range attempts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			release, ok := limiter.acquire("same-principal")
			results <- ok
			if !ok {
				return
			}
			<-releaseHeld
			release()
		}()
	}
	close(start)

	acquired := 0
	for range attempts {
		if <-results {
			acquired++
		}
	}
	if acquired != 3 {
		t.Fatalf("concurrent acquisitions = %d, want exactly 3", acquired)
	}
	close(releaseHeld)
	waitGroup.Wait()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.total != 0 || len(limiter.byPrincipal) != 0 {
		t.Fatalf(
			"released limiter state = total:%d principals:%d",
			limiter.total,
			len(limiter.byPrincipal),
		)
	}
}

func TestBothWebSocketHandlersRejectCapacityBeforeUpgrade(t *testing.T) {
	limiter := newWebSocketConnectionLimiter(1, 1)
	release, ok := limiter.acquire("ip:192.0.2.10")
	if !ok {
		t.Fatal("failed to occupy test connection slot")
	}
	defer release()

	request := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "192.0.2.10:12345"
		return req
	}

	translationResponse := httptest.NewRecorder()
	(&WebSocketHandler{connections: limiter}).Handle(
		translationResponse,
		request("/ws/translate"),
	)
	if translationResponse.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"translation status = %d, want %d",
			translationResponse.Code,
			http.StatusTooManyRequests,
		)
	}

	speechmaticsResponse := httptest.NewRecorder()
	(&SpeechmaticsProxyHandler{connections: limiter}).HandleProxy(
		speechmaticsResponse,
		request("/ws/speechmatics"),
	)
	if speechmaticsResponse.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"Speechmatics status = %d, want %d",
			speechmaticsResponse.Code,
			http.StatusTooManyRequests,
		)
	}
}

func TestWebSocketHandlersUseOneSharedDefaultLimiter(t *testing.T) {
	t.Setenv("SM_API_KEY", "test-only")
	translation := NewWebSocketHandler(nil)
	speechmatics, err := NewSpeechmaticsProxyHandler(nil)
	if err != nil {
		t.Fatal(err)
	}
	if translation.connections == nil ||
		translation.connections != speechmatics.connections {
		t.Fatal("translation and Speechmatics handlers do not share a connection limiter")
	}
	if translation.billing != nil || speechmatics.billing != nil {
		t.Fatal("typed nil billing service enabled WebSocket billing mode")
	}
}

func TestPositiveWebSocketLimitFromEnvRejectsUnsafeValues(t *testing.T) {
	t.Setenv("TEST_WEBSOCKET_LIMIT", "0")
	if got := positiveWebSocketLimitFromEnv("TEST_WEBSOCKET_LIMIT", 7); got != 7 {
		t.Fatalf("zero limit = %d, want fallback 7", got)
	}
	t.Setenv("TEST_WEBSOCKET_LIMIT", "100001")
	if got := positiveWebSocketLimitFromEnv("TEST_WEBSOCKET_LIMIT", 7); got != 7 {
		t.Fatalf("oversized limit = %d, want fallback 7", got)
	}
	t.Setenv("TEST_WEBSOCKET_LIMIT", "5")
	if got := positiveWebSocketLimitFromEnv("TEST_WEBSOCKET_LIMIT", 7); got != 5 {
		t.Fatalf("valid limit = %d, want 5", got)
	}
}
