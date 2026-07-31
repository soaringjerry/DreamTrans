package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestDeleteAdminUserAndCleanupRunsAfterCommittedDeletion(t *testing.T) {
	var events []string
	listSessionIDs := func(
		_ context.Context,
		tenantID, userID string,
	) ([]string, error) {
		if tenantID != "tenant-1" || userID != "target-1" {
			t.Fatalf("list owner = %q/%q", tenantID, userID)
		}
		events = append(events, "list")
		return []string{"session-1", "session-2", "session-3"}, nil
	}
	deleteUser := func(
		_ context.Context,
		targetUserID, actorUserID string,
	) error {
		if targetUserID != "target-1" || actorUserID != "actor-1" {
			t.Fatalf("delete users = target %q actor %q", targetUserID, actorUserID)
		}
		events = append(events, "delete")
		return nil
	}
	cleanupError := errors.New("sqlite unavailable")
	cleanup := func(tenantID, userID, sessionID string) error {
		if tenantID != "tenant-1" || userID != "target-1" {
			t.Fatalf("cleanup owner = %q/%q", tenantID, userID)
		}
		events = append(events, "cleanup:"+sessionID)
		if sessionID == "session-2" {
			return cleanupError
		}
		return nil
	}

	result, failures, err := deleteAdminUserAndCleanup(
		t.Context(),
		"tenant-1",
		"target-1",
		"actor-1",
		listSessionIDs,
		deleteUser,
		cleanup,
	)
	if err != nil {
		t.Fatalf("delete admin user: %v", err)
	}
	if result == nil ||
		result.Status != "partial_failure" ||
		result.Attempted != 3 ||
		result.Failed != 1 {
		t.Fatalf("cleanup result = %#v", result)
	}
	if len(failures) != 1 ||
		failures[0].sessionID != "session-2" ||
		!errors.Is(failures[0].err, cleanupError) {
		t.Fatalf("cleanup failures = %#v", failures)
	}
	wantEvents := []string{
		"list",
		"delete",
		"cleanup:session-1",
		"cleanup:session-2",
		"cleanup:session-3",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

func TestDeleteAdminUserAndCleanupStopsBeforeCommitFailure(t *testing.T) {
	deleteError := errors.New("delete failed")
	cleanupCalled := false

	result, failures, err := deleteAdminUserAndCleanup(
		t.Context(),
		"tenant-1",
		"target-1",
		"actor-1",
		func(context.Context, string, string) ([]string, error) {
			return []string{"session-1"}, nil
		},
		func(context.Context, string, string) error {
			return deleteError
		},
		func(string, string, string) error {
			cleanupCalled = true
			return nil
		},
	)
	if !errors.Is(err, deleteError) {
		t.Fatalf("delete error = %v, want %v", err, deleteError)
	}
	if result != nil || failures != nil {
		t.Fatalf("result = %#v, failures = %#v; want nil", result, failures)
	}
	if cleanupCalled {
		t.Fatal("cleanup ran even though PostgreSQL deletion did not commit")
	}
}

func TestDeleteAdminUserAndCleanupStopsWhenEnumerationFails(t *testing.T) {
	listError := errors.New("list sessions failed")
	deleteCalled := false

	result, failures, err := deleteAdminUserAndCleanup(
		t.Context(),
		"tenant-1",
		"target-1",
		"actor-1",
		func(context.Context, string, string) ([]string, error) {
			return nil, listError
		},
		func(context.Context, string, string) error {
			deleteCalled = true
			return nil
		},
		func(string, string, string) error {
			t.Fatal("cleanup must not run after enumeration failure")
			return nil
		},
	)
	if !errors.Is(err, listError) {
		t.Fatalf("enumeration error = %v, want %v", err, listError)
	}
	if result != nil || failures != nil {
		t.Fatalf("result = %#v, failures = %#v; want nil", result, failures)
	}
	if deleteCalled {
		t.Fatal("PostgreSQL user was deleted despite failed cleanup enumeration")
	}
}

func TestDeleteAdminUserAndCleanupWithoutLegacyStore(t *testing.T) {
	listCalled := false
	deleteCalled := false

	result, failures, err := deleteAdminUserAndCleanup(
		t.Context(),
		"tenant-1",
		"target-1",
		"actor-1",
		func(context.Context, string, string) ([]string, error) {
			listCalled = true
			return nil, nil
		},
		func(context.Context, string, string) error {
			deleteCalled = true
			return nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("delete admin user: %v", err)
	}
	if result != nil || failures != nil {
		t.Fatalf("result = %#v, failures = %#v; want nil", result, failures)
	}
	if listCalled {
		t.Fatal("enumerated sessions even though no legacy cleanup is configured")
	}
	if !deleteCalled {
		t.Fatal("PostgreSQL user deletion did not run")
	}
}

func TestWriteDeleteAdminUserSuccessReportsPartialLegacyCleanup(t *testing.T) {
	response := httptest.NewRecorder()
	writeDeleteAdminUserSuccess(response, &legacyRAGCleanupResult{
		Status:    "partial_failure",
		Attempted: 3,
		Failed:    1,
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body deleteAdminUserResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success ||
		body.LegacyRAGCleanup == nil ||
		body.LegacyRAGCleanup.Status != "partial_failure" ||
		body.LegacyRAGCleanup.Attempted != 3 ||
		body.LegacyRAGCleanup.Failed != 1 {
		t.Fatalf("response = %#v", body)
	}
}
