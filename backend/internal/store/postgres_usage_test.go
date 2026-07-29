package store

import (
	"math"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
)

func TestValidateUsageLogRejectsInvalidQuantitiesAndActions(t *testing.T) {
	tests := []*models.UsageLog{
		nil,
		{TenantID: "tenant-1", UserID: "user-1", Action: "unknown", Quantity: 1},
		{TenantID: "tenant-1", UserID: "user-1", Action: "storage", Quantity: -1},
		{TenantID: "tenant-1", UserID: "user-1", Action: "storage", Quantity: math.NaN()},
		{TenantID: "tenant-1", UserID: "user-1", Action: "storage", Quantity: math.Inf(1)},
	}
	for index, usage := range tests {
		if err := validateUsageLog(usage); err == nil {
			t.Fatalf("case %d: expected validation error", index)
		}
	}
}

func TestValidateUsageLogNormalizesOptionalSession(t *testing.T) {
	blankSession := "  "
	usage := &models.UsageLog{
		TenantID:  " tenant-1 ",
		UserID:    " user-1 ",
		Action:    " transcription ",
		Quantity:  1,
		SessionID: &blankSession,
	}
	if err := validateUsageLog(usage); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if usage.TenantID != "tenant-1" || usage.UserID != "user-1" || usage.Action != "transcription" {
		t.Fatalf("usage was not normalized: %#v", usage)
	}
	if usage.SessionID != nil {
		t.Fatalf("blank session id should normalize to nil: %#v", usage.SessionID)
	}
}

func TestAPIQuotaExceeded(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		used  int64
		want  bool
	}{
		{name: "unlimited", limit: -1, used: 1 << 40, want: false},
		{name: "zero", limit: 0, used: 0, want: true},
		{name: "remaining", limit: 10, used: 9, want: false},
		{name: "at limit", limit: 10, used: 10, want: true},
		{name: "over limit", limit: 10, used: 11, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := apiQuotaExceeded(test.limit, test.used); got != test.want {
				t.Fatalf("apiQuotaExceeded(%d, %d) = %v, want %v",
					test.limit, test.used, got, test.want)
			}
		})
	}
}
