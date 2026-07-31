package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/dreamtrans/backend/internal/modelcatalog"
)

func TestModelRefreshErrorStatus(t *testing.T) {
	if got := modelRefreshErrorStatus(errors.New("database write failed")); got != http.StatusInternalServerError {
		t.Fatalf("database error status = %d, want %d", got, http.StatusInternalServerError)
	}
	providerErr := fmt.Errorf("fetch models: %w", modelcatalog.ErrProviderUnavailable)
	if got := modelRefreshErrorStatus(providerErr); got != http.StatusBadGateway {
		t.Fatalf("provider error status = %d, want %d", got, http.StatusBadGateway)
	}
}
