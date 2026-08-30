package handlers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/billing"
)

func TestSequenceProgressBroadcastsToConcurrentWaiters(t *testing.T) {
	progress := newSequenceProgress()
	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	const waiterCount = 3
	var ready sync.WaitGroup
	ready.Add(waiterCount)
	results := make(chan bool, waiterCount)
	for range waiterCount {
		go func() {
			ready.Done()
			results <- progress.Wait(waitCtx, 1)
		}()
	}
	ready.Wait()
	time.Sleep(10 * time.Millisecond)
	progress.Mark(1)
	for range waiterCount {
		if !<-results {
			t.Fatal("a concurrent delivery waiter missed the progress broadcast")
		}
	}
}

func TestRealtimeOutputReservationMatchesProviderLimit(t *testing.T) {
	for _, source := range []string{"", "short", string(make([]byte, 256*1024))} {
		if got := realtimeOutputReservationTokens(source); got != 8192 {
			t.Fatalf("output reservation for %d bytes = %d, want 8192", len(source), got)
		}
	}
}

func TestClassifyWebSocketAccountingFailure(t *testing.T) {
	tests := []struct {
		name      string
		failure   websocketAccountingFailure
		errorType string
		retryable bool
	}{
		{
			name: "insufficient balance",
			failure: classifyBillingAccountingFailure(
				fmt.Errorf("%w: 0.1 < 0.2", billing.ErrInsufficientBalance),
			),
			errorType: "insufficient_balance",
			retryable: false,
		},
		{
			name:      "pricing unavailable",
			failure:   classifyBillingAccountingFailure(billing.ErrProviderCostNotFound),
			errorType: "pricing_unavailable",
			retryable: false,
		},
		{
			name:      "billing store unavailable",
			failure:   classifyBillingAccountingFailure(errors.New("connection reset")),
			errorType: "billing_temporarily_unavailable",
			retryable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.failure.ErrorType != test.errorType ||
				test.failure.Retryable != test.retryable {
				t.Fatalf("failure = %#v", test.failure)
			}
			if test.retryable && test.failure.RetryAfterMs <= 0 {
				t.Fatalf("retryable failure has no retry delay: %#v", test.failure)
			}
		})
	}
}

func TestProviderFlowGateRetainsFirstTypedFailure(t *testing.T) {
	var gate providerFlowGate
	first := accountingUncertainFailure(errors.New("first settlement timeout"))
	stored, accepted := gate.FailClosed(first)
	if !accepted || stored.ErrorType != "accounting_uncertain" ||
		!stored.ConnectionTerminal || !stored.Retryable {
		t.Fatalf("first failure = %#v accepted=%v", stored, accepted)
	}
	second := accountingUncertainFailure(errors.New("later refund timeout"))
	stored, accepted = gate.FailClosed(second)
	if accepted || stored.Cause.Error() != first.Cause.Error() {
		t.Fatalf("gate replaced first failure: %#v accepted=%v", stored, accepted)
	}
}

func TestTerminalAccountingUncertaintyRequiresManualRecovery(t *testing.T) {
	failure := terminalAccountingUncertainFailure(errors.New("refund outcome unknown"))
	if failure.ErrorType != "accounting_uncertain" ||
		failure.Retryable ||
		failure.RetryAfterMs != 0 ||
		!failure.ConnectionTerminal {
		t.Fatalf("terminal accounting failure = %#v", failure)
	}
}

func TestTranslationErrorResponseIncludesRetrySemantics(t *testing.T) {
	result := accountingTranslateResult(
		&translateJob{seq: 7, requestID: "request-7"},
		websocketAccountingFailure{
			ErrorType:          "accounting_uncertain",
			Reason:             "reconnect",
			Retryable:          true,
			RetryAfterMs:       1500,
			ConnectionTerminal: true,
		},
	)
	response := translationErrorResponse(&result)
	if response["retryable"] != true ||
		response["connection_terminal"] != true ||
		response["retry_after_ms"] != 1500 ||
		response["type"] != "accounting_uncertain" ||
		response["request_id"] != "request-7" {
		t.Fatalf("translation error response = %#v", response)
	}
}
