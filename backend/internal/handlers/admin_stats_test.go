package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/store"
	"github.com/lib/pq"
)

func TestAdminSystemStatsContractKeepsBasicCountsWhenBillingFails(t *testing.T) {
	response := buildAdminSystemStatsResponse(map[string]interface{}{
		"user_count":       3,
		"tenant_count":     1,
		"session_count":    4,
		"transcript_count": 7,
	}, nil, errors.New("billing unavailable"))

	if response.Basic.UserCount != 3 ||
		response.Basic.TenantCount != 1 ||
		response.Basic.SessionCount != 4 ||
		response.Basic.TranscriptCount != 7 {
		t.Fatalf("basic counts changed after billing failure: %#v", response.Basic)
	}
	if response.BillingError == "" {
		t.Fatal("billing failure was not localized in the response")
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &contract); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"basic", "billing", "time"} {
		if _, ok := contract[key]; !ok {
			t.Fatalf("admin stats contract is missing %q: %s", key, encoded)
		}
	}
	if _, leaked := contract["user_count"]; leaked {
		t.Fatalf("basic fields leaked into the top-level contract: %s", encoded)
	}
}

func TestAdminSystemStatsContractReadsExactPostgresCounts(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Scheme == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL must be a PostgreSQL URL")
	}

	setupDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setupDB.Close() })

	schema := fmt.Sprintf("admin_stats_%d", time.Now().UnixNano())
	quotedSchema := pq.QuoteIdentifier(schema)
	if _, err := setupDB.ExecContext(t.Context(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = setupDB.ExecContext(
			context.Background(),
			"DROP SCHEMA "+quotedSchema+" CASCADE",
		)
	})
	for _, statement := range []string{
		"CREATE TABLE " + quotedSchema + ".users (id INTEGER PRIMARY KEY)",
		"CREATE TABLE " + quotedSchema + ".tenants (id INTEGER PRIMARY KEY)",
		"CREATE TABLE " + quotedSchema + ".sessions (id INTEGER PRIMARY KEY)",
		"CREATE TABLE " + quotedSchema + ".transcripts (id INTEGER PRIMARY KEY)",
		"INSERT INTO " + quotedSchema + ".users SELECT generate_series(1, 3)",
		"INSERT INTO " + quotedSchema + ".tenants VALUES (1)",
		"INSERT INTO " + quotedSchema + ".sessions SELECT generate_series(1, 4)",
		"INSERT INTO " + quotedSchema + ".transcripts SELECT generate_series(1, 7)",
	} {
		if _, err := setupDB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}

	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", parsed.String())
	postgresStore, err := store.NewPostgresStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.Close() })

	handler := NewAdminHandler(postgresStore, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/stats", nil)
	recorder := httptest.NewRecorder()
	handler.HandleGetSystemStats(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response AdminSystemStatsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Basic != (AdminBasicStats{
		UserCount: 3, TenantCount: 1, SessionCount: 4, TranscriptCount: 7,
	}) {
		t.Fatalf("unexpected PostgreSQL-backed overview counts: %#v", response.Basic)
	}
}
