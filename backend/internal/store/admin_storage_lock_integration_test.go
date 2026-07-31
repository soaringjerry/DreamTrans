package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestPostgresAIStorageWritersTakeUserGateBeforeTenantQuotaOptIn exercises the
// lock order shared with administrative user deletion. A writer is held on the
// tenant quota row and must already prevent deletion of its owner; otherwise
// DELETE users can hold the user row while transcript cascades wait for the
// tenant and deadlock with the writer's later user FK check.
func TestPostgresAIStorageWritersTakeUserGateBeforeTenantQuotaOptIn(
	t *testing.T,
) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	postgresStore := &PostgresStore{db: db}
	if err := postgresStore.VerifySchema(t.Context()); err != nil {
		t.Fatalf("verify migrated schema: %v", err)
	}

	type storageWriter func(
		context.Context,
		*PostgresStore,
		string,
		string,
		string,
	) error
	writers := []struct {
		name string
		run  storageWriter
	}{
		{
			name: "artifact",
			run: func(
				ctx context.Context,
				store *PostgresStore,
				tenantID, userID, projectID string,
			) error {
				_, err := store.CreateAIArtifactIdempotent(
					ctx,
					&models.AIArtifact{
						TenantID:     tenantID,
						UserID:       userID,
						ProjectID:    &projectID,
						ArtifactType: "summary",
						Title:        "lock-order artifact",
						Content:      "content",
						ContextPolicy: map[string]any{
							"mode": "smart",
						},
					},
				)
				return err
			},
		},
		{
			name: "uploaded source",
			run: func(
				ctx context.Context,
				store *PostgresStore,
				tenantID, userID, projectID string,
			) error {
				return store.CreateKnowledgeSource(
					ctx,
					&models.KnowledgeSource{
						ProjectID:  projectID,
						TenantID:   tenantID,
						UserID:     userID,
						SourceType: "file",
						Name:       "lock-order.txt",
						MediaType:  "text/plain",
						SizeBytes:  7,
						SHA256:     strings.Repeat("a", 64),
						Status:     "queued",
					},
				)
			},
		},
		{
			name: "explicit memory",
			run: func(
				ctx context.Context,
				store *PostgresStore,
				tenantID, userID, projectID string,
			) error {
				return store.CreateMemorySourceWithChunks(
					ctx,
					&models.KnowledgeSource{
						ProjectID:  projectID,
						TenantID:   tenantID,
						UserID:     userID,
						SourceType: "memory",
						Name:       "lock-order memory",
						Content:    "content",
					},
					[]models.KnowledgeChunk{{
						ProjectID:  projectID,
						Ordinal:    0,
						Content:    "content",
						TokenCount: 7,
					}},
				)
			},
		},
	}

	for _, writer := range writers {
		writer := writer
		t.Run(writer.name, func(t *testing.T) {
			tenantID := uuid.NewString()
			actorID := uuid.NewString()
			targetUserID := uuid.NewString()
			sessionID := uuid.NewString()
			projectID := uuid.NewString()
			suffix := strings.ReplaceAll(tenantID, "-", "")
			if _, err := db.ExecContext(t.Context(), `
				INSERT INTO tenants (
					id, name, slug, plan, api_quota_monthly,
					storage_quota_gb, max_sessions
				) VALUES (
					$1,'AI storage lock order',$2,'pro',1000,1,10
				)
			`, tenantID, "ai-storage-lock-"+suffix); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = db.ExecContext(context.Background(), `
					DELETE FROM tenants WHERE id=$1
				`, tenantID)
			})
			if _, err := db.ExecContext(t.Context(), `
				INSERT INTO users (
					id, tenant_id, email, password_hash, name, role,
					is_active, email_verified
				) VALUES
					($1,$3,$4,'integration-only','Deletion actor',
					 'admin',true,true),
					($2,$3,$5,'integration-only','Storage owner',
					 'user',true,true)
			`,
				actorID,
				targetUserID,
				tenantID,
				"storage-lock-actor-"+suffix+"@example.invalid",
				"storage-lock-target-"+suffix+"@example.invalid",
			); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `
				INSERT INTO sessions (
					id, user_id, tenant_id, title, source_language,
					target_language, status
				) VALUES (
					$1,$2,$3,'delete cascade fixture','en','zh','completed'
				)
			`, sessionID, targetUserID, tenantID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `
				INSERT INTO transcripts (
					session_id, speaker, text, start_time, end_time, status,
					is_partial
				) VALUES (
					$1,'Speaker','content',0,1,'confirmed',false
				)
			`, sessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `
				INSERT INTO ai_projects (
					id, tenant_id, user_id, name, context_mode,
					max_context_tokens
				) VALUES (
					$1,$2,$3,'storage writer project','smart',64000
				)
			`, projectID, tenantID, targetUserID); err != nil {
				t.Fatal(err)
			}

			tenantBlocker, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tenantBlocker.Rollback() }()
			var lockedTenantID string
			if err := tenantBlocker.QueryRowContext(t.Context(), `
				SELECT id FROM tenants WHERE id=$1 FOR UPDATE
			`, tenantID).Scan(&lockedTenantID); err != nil {
				t.Fatal(err)
			}

			writerResult := make(chan error, 1)
			go func() {
				writerResult <- writer.run(
					context.Background(),
					postgresStore,
					tenantID,
					targetUserID,
					projectID,
				)
			}()
			waitForAIStorageUserGate(
				t,
				db,
				targetUserID,
				writerResult,
			)

			deleteResult := make(chan error, 1)
			go func() {
				deleteResult <- postgresStore.DeleteUserAdminSafe(
					context.Background(),
					targetUserID,
					actorID,
				)
			}()
			time.Sleep(50 * time.Millisecond)
			if err := tenantBlocker.Commit(); err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-writerResult:
				if err != nil {
					t.Fatalf("storage writer: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("storage writer did not continue after tenant release")
			}
			select {
			case err := <-deleteResult:
				if err != nil {
					t.Fatalf("delete storage owner: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("account deletion deadlocked with storage writer")
			}
		})
	}
}

func waitForAIStorageUserGate(
	t *testing.T,
	db *sql.DB,
	userID string,
	writerResult <-chan error,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-writerResult:
			t.Fatalf(
				"storage writer returned before taking the user gate: %v",
				err,
			)
		default:
		}
		probeTx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := probeTx.ExecContext(t.Context(), `
			SET LOCAL lock_timeout='50ms'
		`); err != nil {
			_ = probeTx.Rollback()
			t.Fatal(err)
		}
		var lockedUserID string
		probeErr := probeTx.QueryRowContext(t.Context(), `
			SELECT id FROM users WHERE id=$1 FOR UPDATE
		`, userID).Scan(&lockedUserID)
		_ = probeTx.Rollback()
		if probeErr != nil {
			if strings.Contains(
				probeErr.Error(),
				"canceling statement due to lock timeout",
			) {
				return
			}
			t.Fatal(probeErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"storage writer did not acquire the user gate for %s",
		userID,
	)
}
