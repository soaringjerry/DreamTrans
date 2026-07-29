package rag

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultRAGMaxDatabaseMB  int64 = 102_400
	minRAGMaxDatabaseMB      int64 = 64
	maxRAGMaxDatabaseMB      int64 = 4_000_000
	defaultSQLitePageSize    int64 = 4_096
	sqliteMaximumPageCount   int64 = 4_294_967_294
	sqliteBusyTimeoutMS      int64 = 5_000
	minRAGJournalBudgetBytes int64 = 4 * 1024 * 1024
	maxRAGJournalBudgetBytes int64 = 64 * 1024 * 1024
	ragSQLiteSHMReserveBytes int64 = 1 * 1024 * 1024
	sqliteWALHeaderBytes     int64 = 32
	sqliteWALFrameBytes      int64 = 24
)

type sqliteStorageConfig struct {
	maxTotalMB             int64
	totalBudgetBytes       int64
	mainDatabaseLimitBytes int64
	shmReserveBytes        int64
	maxPageCount           int64
	journalLimitBytes      int64
	autoCheckpoint         int64
}

func normalizeSQLitePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("RAG database path must not be empty")
	}
	normalizedPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve RAG database path: %w", err)
	}
	return filepath.Clean(normalizedPath), nil
}

func loadRAGMaxDatabaseMB() (int64, error) {
	maxMB := defaultRAGMaxDatabaseMB
	if raw := strings.TrimSpace(os.Getenv("RAG_MAX_DB_MB")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || (parsed != -1 && (parsed < minRAGMaxDatabaseMB || parsed > maxRAGMaxDatabaseMB)) {
			return 0, fmt.Errorf("RAG_MAX_DB_MB must be -1 or between 64 and 4000000")
		}
		maxMB = parsed
	}
	return maxMB, nil
}

func detectSQLitePageSize(path string) (int64, error) {
	file, err := os.Open(path) //nolint:gosec // operator-controlled RAG database path
	if errors.Is(err, os.ErrNotExist) {
		return defaultSQLitePageSize, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect RAG database: %w", err)
	}
	defer func() { _ = file.Close() }()

	header := make([]byte, 18)
	n, err := io.ReadFull(file, header)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if n == 0 {
			return defaultSQLitePageSize, nil
		}
		// Let SQLite produce the authoritative corruption error when it opens
		// the incomplete file.
		return defaultSQLitePageSize, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read RAG database header: %w", err)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		// This also deliberately defers the canonical "not a database" error to
		// SQLite rather than inventing a subtly different compatibility check.
		return defaultSQLitePageSize, nil
	}
	rawPageSize := binary.BigEndian.Uint16(header[16:18])
	pageSize := int64(rawPageSize)
	if rawPageSize == 1 {
		pageSize = 65_536
	}
	if !validSQLitePageSize(pageSize) {
		return defaultSQLitePageSize, nil
	}
	return pageSize, nil
}

func validSQLitePageSize(pageSize int64) bool {
	return pageSize >= 512 && pageSize <= 65_536 && pageSize&(pageSize-1) == 0
}

func makeSQLiteStorageConfig(maxDatabaseMB, pageSize int64) sqliteStorageConfig {
	maxPageCount := sqliteMaximumPageCount
	budgetBaseBytes := defaultRAGMaxDatabaseMB * 1024 * 1024
	if maxDatabaseMB != -1 {
		budgetBaseBytes = maxDatabaseMB * 1024 * 1024
	}

	journalLimitBytes := budgetBaseBytes / 16
	if journalLimitBytes < minRAGJournalBudgetBytes {
		journalLimitBytes = minRAGJournalBudgetBytes
	}
	if journalLimitBytes > maxRAGJournalBudgetBytes {
		journalLimitBytes = maxRAGJournalBudgetBytes
	}

	totalBudgetBytes := int64(-1)
	mainDatabaseLimitBytes := int64(-1)
	shmReserveBytes := ragSQLiteSHMReserveBytes
	if maxDatabaseMB != -1 {
		totalBudgetBytes = budgetBaseBytes
		mainDatabaseLimitBytes = totalBudgetBytes - journalLimitBytes - shmReserveBytes
		maxPageCount = mainDatabaseLimitBytes / pageSize
		if maxPageCount > sqliteMaximumPageCount {
			maxPageCount = sqliteMaximumPageCount
		}
	}

	// Trigger SQLite's automatic checkpoint at roughly half of the retained
	// WAL budget. The frame overhead matters for large page-size databases.
	autoCheckpoint := (journalLimitBytes/2 - sqliteWALHeaderBytes) / (pageSize + sqliteWALFrameBytes)
	if autoCheckpoint < 1 {
		autoCheckpoint = 1
	}

	return sqliteStorageConfig{
		maxTotalMB:             maxDatabaseMB,
		totalBudgetBytes:       totalBudgetBytes,
		mainDatabaseLimitBytes: mainDatabaseLimitBytes,
		shmReserveBytes:        shmReserveBytes,
		maxPageCount:           maxPageCount,
		journalLimitBytes:      journalLimitBytes,
		autoCheckpoint:         autoCheckpoint,
	}
}

func openConfiguredSQLite(path string, config sqliteStorageConfig) (*sql.DB, error) {
	dsn := configuredSQLiteDSN(path, config)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection is sufficient for this local index and prevents a reader
	// from indefinitely blocking the bounded WAL checkpoints. Crucially, the
	// DSN below reapplies every connection-local PRAGMA if database/sql ever
	// replaces this connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open RAG database: %w", err)
	}
	return db, nil
}

func configuredSQLiteDSN(path string, config sqliteStorageConfig) string {
	fileURL := &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	values := url.Values{}
	values.Set("_busy_timeout", strconv.FormatInt(sqliteBusyTimeoutMS, 10))
	values.Set("_foreign_keys", "on")
	values.Set("_journal_mode", "WAL")
	values.Add("_pragma", fmt.Sprintf("journal_size_limit=%d", config.journalLimitBytes))
	values.Add("_pragma", fmt.Sprintf("max_page_count=%d", config.maxPageCount))
	values.Add("_pragma", fmt.Sprintf("wal_autocheckpoint=%d", config.autoCheckpoint))
	fileURL.RawQuery = values.Encode()
	return fileURL.String()
}

func readSQLiteIntPragma(db *sql.DB, name string) (int64, error) {
	var value int64
	// name is always a package constant/call-site literal. SQLite PRAGMA names
	// cannot be supplied as bound parameters.
	//nolint:gosec // G202: no request or environment input reaches name.
	if err := db.QueryRow(`PRAGMA ` + name + `;`).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

func verifySQLiteSettings(db *sql.DB, config sqliteStorageConfig) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("verify SQLite connection settings: %w", err)
	}
	defer func() { _ = conn.Close() }()

	checkInt := func(name string, want int64) error {
		var got int64
		// All names and values originate from package constants and validated
		// integer calculations, not request data.
		//nolint:gosec // G202: no untrusted input reaches this PRAGMA.
		if err := conn.QueryRowContext(context.Background(), `PRAGMA `+name+`;`).Scan(&got); err != nil {
			return fmt.Errorf("read SQLite %s: %w", name, err)
		}
		if got != want {
			return fmt.Errorf("SQLite %s = %d, want %d", name, got, want)
		}
		return nil
	}
	if err := checkInt("foreign_keys", 1); err != nil {
		return err
	}
	if err := checkInt("busy_timeout", sqliteBusyTimeoutMS); err != nil {
		return err
	}
	if err := checkInt("journal_size_limit", config.journalLimitBytes); err != nil {
		return err
	}
	if err := checkInt("wal_autocheckpoint", config.autoCheckpoint); err != nil {
		return err
	}

	var journalMode string
	if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		return fmt.Errorf("read SQLite journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("SQLite journal_mode = %q, want WAL", journalMode)
	}

	var appliedMaxPages int64
	if err := conn.QueryRowContext(context.Background(), `PRAGMA max_page_count;`).Scan(&appliedMaxPages); err != nil {
		return fmt.Errorf("read SQLite maximum page count: %w", err)
	}
	if config.maxTotalMB != -1 && appliedMaxPages > config.maxPageCount {
		return fmt.Errorf(
			"existing RAG main database exceeds its %d-byte share of the "+
				"RAG_MAX_DB_MB total budget (%d MB); increase RAG_MAX_DB_MB",
			config.mainDatabaseLimitBytes,
			config.maxTotalMB,
		)
	}
	if appliedMaxPages != config.maxPageCount {
		return fmt.Errorf(
			"SQLite max_page_count = %d, want %d",
			appliedMaxPages,
			config.maxPageCount,
		)
	}
	return nil
}
