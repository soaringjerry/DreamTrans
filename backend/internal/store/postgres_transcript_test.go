package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestTranscriptUpsertDoesNotRegressTranslatedSegment(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE transcripts (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			session_id TEXT NOT NULL,
			client_segment_id TEXT NOT NULL,
			speaker TEXT,
			text TEXT NOT NULL,
			translation TEXT,
			start_time REAL NOT NULL,
			end_time REAL,
			status TEXT NOT NULL,
			is_partial BOOLEAN NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(session_id, client_segment_id)
		)
	`); err != nil {
		t.Fatal(err)
	}

	upsert := func(speaker, text, status string, translation any, start, end float64, partial bool) {
		t.Helper()
		var id, createdAt, updatedAt string
		err := db.QueryRowContext(
			context.Background(), transcriptUpsertQuery,
			"session-1", "segment-1", speaker, text, translation,
			start, end, status, partial,
		).Scan(&id, &createdAt, &updatedAt)
		if err != nil {
			t.Fatal(err)
		}
	}

	upsert("final speaker", "final text", "translated", "保留的翻译", 1, 2, false)
	upsert("stale speaker", "stale confirmed text", "confirmed", nil, 10, 20, false)
	upsert("stale speaker", "stale partial text", "partial", "", 30, 40, true)

	var speaker, text, translation, status string
	var start, end float64
	var partial bool
	if err := db.QueryRow(`
		SELECT speaker, text, translation, start_time, end_time, status, is_partial
		FROM transcripts
		WHERE session_id = ? AND client_segment_id = ?
	`, "session-1", "segment-1").Scan(&speaker, &text, &translation, &start, &end, &status, &partial); err != nil {
		t.Fatal(err)
	}
	if speaker != "final speaker" || text != "final text" || start != 1 || end != 2 {
		t.Fatalf("final content regressed: speaker=%q text=%q start=%v end=%v", speaker, text, start, end)
	}
	if translation != "保留的翻译" {
		t.Fatalf("translation = %q, want preserved translation", translation)
	}
	if status != "translated" {
		t.Fatalf("status = %q, want translated", status)
	}
	if partial {
		t.Fatal("translated segment regressed to partial")
	}

	upsert("corrected speaker", "corrected text", "translated", "更正的翻译", 3, 4, false)
	if err := db.QueryRow(`
		SELECT speaker, text, translation, start_time, end_time, status, is_partial
		FROM transcripts
		WHERE session_id = ? AND client_segment_id = ?
	`, "session-1", "segment-1").Scan(&speaker, &text, &translation, &start, &end, &status, &partial); err != nil {
		t.Fatal(err)
	}
	if speaker != "corrected speaker" || text != "corrected text" ||
		translation != "更正的翻译" || start != 3 || end != 4 {
		t.Fatalf("same-status correction was ignored: speaker=%q text=%q translation=%q start=%v end=%v",
			speaker, text, translation, start, end)
	}
}

func TestRegisterBatchJobRejectsConflictingReservation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE batch_transcription_jobs (
			job_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			reservation_key TEXT,
			completed_at TEXT
		);
		CREATE UNIQUE INDEX idx_batch_jobs_reservation_key
			ON batch_transcription_jobs(reservation_key)
			WHERE reservation_key IS NOT NULL;
	`); err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{db: db}
	ctx := context.Background()
	if err := store.RegisterBatchJob(ctx, "job-1", "user-1", "tenant-1", "reservation-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterBatchJob(ctx, "job-1", "user-1", "tenant-1", "reservation-1"); err != nil {
		t.Fatalf("idempotent registration failed: %v", err)
	}
	if err := store.RegisterBatchJob(ctx, "job-1", "user-1", "tenant-1", "reservation-2"); !errors.Is(err, ErrBatchJobConflict) {
		t.Fatalf("reservation collision error = %v, want ErrBatchJobConflict", err)
	}
	if err := store.RegisterBatchJob(ctx, "job-1", "user-2", "tenant-1", "reservation-1"); !errors.Is(err, ErrBatchJobConflict) {
		t.Fatalf("owner collision error = %v, want ErrBatchJobConflict", err)
	}
	key, completed, err := store.GetBatchJobBillingState(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if key != "reservation-1" || completed {
		t.Fatalf("unexpected initial billing state: key=%q completed=%v", key, completed)
	}
	if err := store.MarkBatchJobCompleted(ctx, "job-1", "user-1"); err != nil {
		t.Fatal(err)
	}
	_, completed, err = store.GetBatchJobBillingState(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("completed state was not persisted")
	}
}

func TestExceedsStorageQuota(t *testing.T) {
	tests := []struct {
		name      string
		quotaGB   int
		usedBytes int64
		want      bool
	}{
		{name: "unlimited", quotaGB: -1, usedBytes: 1 << 62, want: false},
		{name: "zero empty", quotaGB: 0, usedBytes: 0, want: false},
		{name: "zero nonempty", quotaGB: 0, usedBytes: 1, want: true},
		{name: "exact limit", quotaGB: 1, usedBytes: bytesPerGiB, want: false},
		{name: "over limit", quotaGB: 1, usedBytes: bytesPerGiB + 1, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exceedsStorageQuota(test.quotaGB, test.usedBytes); got != test.want {
				t.Fatalf("exceedsStorageQuota(%d, %d) = %v, want %v",
					test.quotaGB, test.usedBytes, got, test.want)
			}
		})
	}
}
