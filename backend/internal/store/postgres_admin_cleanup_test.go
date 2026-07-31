package store

import (
	"database/sql"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func TestListSessionIDsByOwnerScopesTenantAndUser(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id       string
		tenantID string
		userID   string
	}{
		{id: "session-b", tenantID: "tenant-1", userID: "user-1"},
		{id: "session-a", tenantID: "tenant-1", userID: "user-1"},
		{id: "wrong-user", tenantID: "tenant-1", userID: "user-2"},
		{id: "wrong-tenant", tenantID: "tenant-2", userID: "user-1"},
	} {
		if _, err := db.Exec(
			`INSERT INTO sessions (id, tenant_id, user_id) VALUES (?, ?, ?)`,
			fixture.id,
			fixture.tenantID,
			fixture.userID,
		); err != nil {
			t.Fatal(err)
		}
	}

	postgresStore := &PostgresStore{db: db}
	sessionIDs, err := postgresStore.ListSessionIDsByOwner(
		t.Context(),
		"tenant-1",
		"user-1",
	)
	if err != nil {
		t.Fatalf("list session IDs: %v", err)
	}
	want := []string{"session-a", "session-b"}
	if !reflect.DeepEqual(sessionIDs, want) {
		t.Fatalf("session IDs = %#v, want %#v", sessionIDs, want)
	}
}
