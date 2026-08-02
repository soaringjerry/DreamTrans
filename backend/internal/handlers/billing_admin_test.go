package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/dreamtrans/backend/internal/billing"
)

func TestBillingAdminErrorStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "invalid input",
			err:  fmt.Errorf("wrapped: %w", billing.ErrInvalidBillingInput),
			want: http.StatusBadRequest,
		},
		{
			name: "stale preview",
			err:  fmt.Errorf("wrapped: %w", billing.ErrBillingPreviewStale),
			want: http.StatusConflict,
		},
		{name: "missing resource", err: sql.ErrNoRows, want: http.StatusNotFound},
		{name: "database failure", err: errors.New("database failed"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := billingAdminErrorStatus(test.err); got != test.want {
				t.Fatalf("billingAdminErrorStatus() = %d, want %d", got, test.want)
			}
		})
	}
}
