package rag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInsertDocumentUpdatesSingleEmbedding(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	document := &Document{
		SessionID: "session-1",
		Speaker:   "speaker",
		StartTime: 1,
		EndTime:   2,
		Original:  "same paragraph",
		Summary:   "summary",
		CreatedAt: time.Now(),
	}
	firstID, err := store.InsertDocumentWithEmbedding(document, []float32{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := store.InsertDocumentWithEmbedding(document, []float32{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("document IDs differ: %d and %d", firstID, secondID)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE doc_id = ?`, firstID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("embedding count = %d, want 1", count)
	}

	vectors, err := store.LoadEmbeddingsForDocs([]int64{firstID})
	if err != nil {
		t.Fatal(err)
	}
	got := vectors[firstID]
	if len(got) != 3 || got[0] != 4 {
		t.Fatalf("embedding was not updated: %#v", got)
	}
}

func TestRAGDatabaseSizeLimitConfiguration(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("RAG_MAX_DB_MB", "not-a-number")
		store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
		if err == nil {
			_ = store.Close()
			t.Fatal("invalid RAG_MAX_DB_MB was accepted")
		}
	})

	t.Run("applied", func(t *testing.T) {
		t.Setenv("RAG_MAX_DB_MB", "64")
		store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		var pageSize, maxPages int64
		if err := store.db.QueryRow(`PRAGMA page_size;`).Scan(&pageSize); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`PRAGMA max_page_count;`).Scan(&maxPages); err != nil {
			t.Fatal(err)
		}
		config := makeSQLiteStorageConfig(64, pageSize)
		const totalBudget = int64(64 * 1024 * 1024)
		if config.mainDatabaseLimitBytes != 59*1024*1024 {
			t.Fatalf("main database budget = %d, want 59 MiB", config.mainDatabaseLimitBytes)
		}
		if config.journalLimitBytes != 4*1024*1024 {
			t.Fatalf("WAL/journal budget = %d, want 4 MiB", config.journalLimitBytes)
		}
		if config.shmReserveBytes != 1*1024*1024 {
			t.Fatalf("SHM reserve = %d, want 1 MiB", config.shmReserveBytes)
		}
		if got := config.mainDatabaseLimitBytes + config.journalLimitBytes + config.shmReserveBytes; got != totalBudget {
			t.Fatalf("split storage budget = %d, want exactly 64 MiB", got)
		}
		if got := pageSize*maxPages + config.journalLimitBytes + config.shmReserveBytes; got > totalBudget {
			t.Fatalf("main + sidecar limits = %d bytes, want at most 64 MiB", got)
		}
		diskUsage, _, err := store.sqliteDiskUsage()
		if err != nil {
			t.Fatal(err)
		}
		if diskUsage > config.totalBudgetBytes {
			t.Fatalf("fresh database disk usage = %d, budget %d", diskUsage, config.totalBudgetBytes)
		}
	})

	t.Run("explicit unlimited removes previous limit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rag.db")
		t.Setenv("RAG_MAX_DB_MB", "64")
		store, err := NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}

		t.Setenv("RAG_MAX_DB_MB", "-1")
		store, err = NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := store.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		}()
		var maxPages int64
		if err := store.db.QueryRow(`PRAGMA max_page_count;`).Scan(&maxPages); err != nil {
			t.Fatal(err)
		}
		if maxPages != sqliteMaximumPageCount {
			t.Fatalf("max_page_count = %d, want SQLite maximum %d", maxPages, sqliteMaximumPageCount)
		}
	})
}

func TestSQLiteSettingsSurviveConnectionReplacementAndReopen(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	path := filepath.Join(t.TempDir(), "rag data", "rag #1?.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	var pageSize int64
	if err := store.db.QueryRow(`PRAGMA page_size;`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	config := makeSQLiteStorageConfig(64, pageSize)

	// With no idle connections, closing sql.Conn below destroys the physical
	// connection. Every assertion therefore exercises a newly opened driver
	// connection and its DSN initialization.
	store.db.SetMaxIdleConns(0)
	assertSQLiteConnectionSettings(t, store.db, config)

	dsn := configuredSQLiteDSN(store.path, config)
	unmanaged, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	tamperedMaxPages := config.maxPageCount + 1_024
	var applied int64
	// tamperedMaxPages is calculated entirely inside this test.
	//nolint:gosec // G202: no untrusted input reaches this PRAGMA.
	if err := unmanaged.QueryRow(fmt.Sprintf(`PRAGMA max_page_count = %d;`, tamperedMaxPages)).Scan(&applied); err != nil {
		_ = unmanaged.Close()
		t.Fatal(err)
	}
	if applied != tamperedMaxPages {
		_ = unmanaged.Close()
		t.Fatalf("tampered max_page_count = %d, want %d", applied, tamperedMaxPages)
	}
	if err := unmanaged.Close(); err != nil {
		t.Fatal(err)
	}

	// The next Store connection must restore max_page_count as well as the
	// connection-local foreign_keys, busy_timeout and WAL controls.
	assertSQLiteConnectionSettings(t, store.db, config)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	}()
	reopened.db.SetMaxIdleConns(0)
	assertSQLiteConnectionSettings(t, reopened.db, config)
}

func TestSQLiteWALOversizeFallbackTruncatesSidecar(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	// Force the fallback threshold below one WAL frame. Normal production
	// thresholds are derived from the configured journal budget.
	store.checkpointTriggerBytes = 1
	if err := store.UpdateSessionSummary("session-1", strings.Repeat("summary ", 256)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil && info.Size() != 0 {
		t.Fatalf("WAL size after forced truncate = %d, want 0", info.Size())
	}
}

func TestMultipleStoresSerializeWritesToSharedWAL(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	path := filepath.Join(t.TempDir(), "rag.db")
	first, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(path)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second store: %v", err)
		}
		if err := first.Close(); err != nil {
			t.Errorf("close first store: %v", err)
		}
	}()

	errs := make(chan error, 2)
	for index, store := range []*Store{first, second} {
		index, store := index, store
		go func() {
			for write := 0; write < 25; write++ {
				if err := store.UpdateSessionSummary(
					fmt.Sprintf("session-%d", index),
					fmt.Sprintf("summary-%d", write),
				); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestExistingNonDefaultSQLitePageSizeIsRespected(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	path := filepath.Join(t.TempDir(), "rag.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA page_size=8192; CREATE TABLE seed (id INTEGER PRIMARY KEY);`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	var pageSize, maxPages int64
	if err := store.db.QueryRow(`PRAGMA page_size;`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA max_page_count;`).Scan(&maxPages); err != nil {
		t.Fatal(err)
	}
	if pageSize != 8_192 {
		t.Fatalf("page_size = %d, want 8192", pageSize)
	}
	config := makeSQLiteStorageConfig(64, pageSize)
	if want := config.mainDatabaseLimitBytes / 8_192; maxPages != want {
		t.Fatalf("max_page_count = %d, want %d", maxPages, want)
	}
}

func TestDocumentHashesUseSHA256AndReadLegacySHA1(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	current := &Document{
		SessionID: "sha256-session",
		Speaker:   "speaker",
		StartTime: 1,
		EndTime:   2,
		Original:  "new SHA-256 paragraph",
		Summary:   "summary",
	}
	currentID, err := store.InsertDocumentWithEmbedding(current, []float32{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	var storedHash string
	if err := store.db.QueryRow(`SELECT hash FROM documents WHERE id=?`, currentID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if want := hashKey(current.SessionID, current.Original, current.StartTime, current.EndTime); storedHash != want {
		t.Fatalf("new document hash = %q, want SHA-256 key %q", storedHash, want)
	}
	if !strings.HasPrefix(storedHash, "sha256:") || len(storedHash) != len("sha256:")+sha256.Size*2 {
		t.Fatalf("new document hash has unexpected SHA-256 encoding: %q", storedHash)
	}
	if storedHash == legacyHashKey(current.SessionID, current.Original, current.StartTime, current.EndTime) {
		t.Fatal("new document was written with the legacy SHA-1 key")
	}
	if exists, err := store.HasDocument(current.SessionID, current.Original, current.StartTime, current.EndTime); err != nil {
		t.Fatal(err)
	} else if !exists {
		t.Fatal("SHA-256 document was not found")
	}

	legacy := &Document{
		SessionID: "legacy-session",
		Speaker:   "speaker",
		StartTime: 3,
		EndTime:   4,
		Original:  "paragraph stored by an old release",
		Summary:   "legacy summary",
	}
	legacyKey := legacyHashKey(legacy.SessionID, legacy.Original, legacy.StartTime, legacy.EndTime)
	res, err := store.db.Exec(`
		INSERT INTO documents(
			session_id, speaker, start_time, end_time,
			original_text, summary, hash, created_at
		) VALUES(?,?,?,?,?,?,?,?)
	`, legacy.SessionID, legacy.Speaker, legacy.StartTime, legacy.EndTime,
		legacy.Original, legacy.Summary, legacyKey, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	legacyID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := store.HasDocument(legacy.SessionID, legacy.Original, legacy.StartTime, legacy.EndTime); err != nil {
		t.Fatal(err)
	} else if !exists {
		t.Fatal("legacy SHA-1 document was not found")
	}

	gotID, err := store.InsertDocumentWithEmbedding(legacy, []float32{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if gotID != legacyID {
		t.Fatalf("legacy document ID = %d after insert, want existing ID %d", gotID, legacyID)
	}
	var count int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM documents WHERE session_id=?`,
		legacy.SessionID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("legacy document count = %d, want 1", count)
	}
	if err := store.db.QueryRow(`SELECT hash FROM documents WHERE id=?`, legacyID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash != legacyKey {
		t.Fatalf("legacy hash was unexpectedly rewritten: %q", storedHash)
	}
}

func TestRAGWritePayloadBound(t *testing.T) {
	if err := validateRAGWriteSize(maxRAGSerializedWriteBytes); err != nil {
		t.Fatalf("maximum write size was rejected: %v", err)
	}
	err := validateRAGWriteSize(maxRAGSerializedWriteBytes, 1)
	if !errors.Is(err, ErrRAGWriteTooLarge) {
		t.Fatalf("oversized write error = %v, want ErrRAGWriteTooLarge", err)
	}
}

func TestSQLiteTotalBudgetIsCheckedBeforeWrite(t *testing.T) {
	t.Setenv("RAG_MAX_DB_MB", "64")
	store, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	store.totalBudgetBytes = 1
	err = store.UpdateSessionSummary("must-not-write", "summary")
	if !errors.Is(err, ErrRAGStorageBudget) {
		t.Fatalf("write error = %v, want ErrRAGStorageBudget", err)
	}
	summary, err := store.GetSessionSummary("must-not-write")
	if err != nil {
		t.Fatal(err)
	}
	if summary != "" {
		t.Fatalf("summary was written despite exhausted storage budget: %q", summary)
	}
}

func assertSQLiteConnectionSettings(t *testing.T, db *sql.DB, config sqliteStorageConfig) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SQLite connection: %v", err)
		}
	}()

	assertIntPragma := func(name string, want int64) {
		t.Helper()
		var got int64
		// Test-controlled name, never request input.
		//nolint:gosec // G202: test helper receives package-owned literals.
		if err := conn.QueryRowContext(context.Background(), `PRAGMA `+name+`;`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
		}
	}
	assertIntPragma("foreign_keys", 1)
	assertIntPragma("busy_timeout", sqliteBusyTimeoutMS)
	assertIntPragma("max_page_count", config.maxPageCount)
	assertIntPragma("journal_size_limit", config.journalLimitBytes)
	assertIntPragma("wal_autocheckpoint", config.autoCheckpoint)

	var journalMode string
	if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want WAL", journalMode)
	}

	_, err = conn.ExecContext(context.Background(), `
		INSERT INTO embeddings(doc_id, dim, norm, vector_json)
		VALUES(987654321, 1, 1, '[1]')
	`)
	if err == nil {
		t.Fatal("foreign-key violation was accepted on a replacement connection")
	}
}
