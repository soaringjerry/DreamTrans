package rag

import (
    "crypto/sha1" //nolint:gosec // used only for non-security dedup hashing
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    _ "modernc.org/sqlite" // sqlite driver for database/sql (pure Go)
    "strings"
)

// Store manages on-disk storage of documents, summaries and embeddings.
type Store struct {
    db *sql.DB
}

// NewStore opens/creates the SQLite database at given path.
func NewStore(path string) (*Store, error) {
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
        return nil, err
    }
    db, err := sql.Open("sqlite", path)
    if err != nil {
        return nil, err
    }
    s := &Store{db: db}
    if err := s.migrate(); err != nil {
        _ = db.Close()
        return nil, err
    }
    return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
    stmts := []string{
        `PRAGMA journal_mode=WAL;`,
        `CREATE TABLE IF NOT EXISTS documents (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            session_id TEXT NOT NULL,
            speaker TEXT,
            start_time REAL,
            end_time REAL,
            original_text TEXT NOT NULL,
            summary TEXT,
            hash TEXT UNIQUE,
            created_at DATETIME NOT NULL
        );`,
        `CREATE INDEX IF NOT EXISTS idx_docs_session_time ON documents(session_id, start_time);`,
        `CREATE TABLE IF NOT EXISTS embeddings (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            doc_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
            dim INTEGER NOT NULL,
            norm REAL NOT NULL,
            vector_json TEXT NOT NULL
        );`,
        `CREATE TABLE IF NOT EXISTS session_summary (
            session_id TEXT PRIMARY KEY,
            summary TEXT,
            updated_at DATETIME NOT NULL
        );`,
    }
    for _, st := range stmts {
        if _, err := s.db.Exec(st); err != nil {
            return err
        }
    }
    // Add title column if not exists (SQLite lacks easy IF NOT EXISTS for ADD COLUMN)
    if _, err := s.db.Exec(`ALTER TABLE session_summary ADD COLUMN title TEXT`); err != nil {
        // ignore duplicate column error; modernc sqlite error text contains 'duplicate column name'
        // other errors should be returned
        if !containsIgnoreCase(err.Error(), "duplicate column") {
            return err
        }
    }
    return nil
}

type Document struct {
    ID          int64
    SessionID   string
    Speaker     string
    StartTime   float64
    EndTime     float64
    Original    string
    Summary     string
    CreatedAt   time.Time
}

func hashKey(sessionID, text string, start, end float64) string {
    sum := sha1.Sum([]byte(text)) //nolint:gosec // non-crypto use: collision tolerance acceptable for dedup
    return fmt.Sprintf("%s|%.3f|%.3f|%x", sessionID, start, end, sum[:])
}

// InsertDocumentWithEmbedding stores a document and its embedding atomically.
func (s *Store) InsertDocumentWithEmbedding(doc *Document, vec []float32) (int64, error) {
    tx, err := s.db.Begin()
    if err != nil { return 0, err }
    defer func() { _ = tx.Rollback() }()

    h := hashKey(doc.SessionID, doc.Original, doc.StartTime, doc.EndTime)
    res, err := tx.Exec(`INSERT OR IGNORE INTO documents(session_id, speaker, start_time, end_time, original_text, summary, hash, created_at)
        VALUES(?,?,?,?,?,?,?,?)`,
        doc.SessionID, doc.Speaker, doc.StartTime, doc.EndTime, doc.Original, doc.Summary, h, time.Now().UTC(),
    )
    if err != nil { return 0, err }
    id, err := res.LastInsertId()
    if err != nil { return 0, err }
    if id == 0 {
        // duplicate
        var existingID int64
        row := tx.QueryRow(`SELECT id FROM documents WHERE hash=?`, h)
        if err := row.Scan(&existingID); err != nil { return 0, err }
        id = existingID
    }
    // store embedding
    // compute norm
    var norm float64
    for _, v := range vec { norm += float64(v*v) }
    // store as JSON
    jb, _ := json.Marshal(vec)
    if _, err := tx.Exec(`INSERT OR REPLACE INTO embeddings(doc_id, dim, norm, vector_json) VALUES(?,?,?,?)`, id, len(vec), norm, string(jb)); err != nil {
        return 0, err
    }
    if err := tx.Commit(); err != nil { return 0, err }
    return id, nil
}

// UpdateSessionSummary upserts the running summary for the session.
func (s *Store) UpdateSessionSummary(sessionID, summary string) error {
    _, err := s.db.Exec(`INSERT INTO session_summary(session_id, summary, updated_at) VALUES(?,?,?)
        ON CONFLICT(session_id) DO UPDATE SET summary=excluded.summary, updated_at=excluded.updated_at`, sessionID, summary, time.Now().UTC())
    return err
}

// GetSessionSummary returns the current summary for the session.
func (s *Store) GetSessionSummary(sessionID string) (string, error) {
    row := s.db.QueryRow(`SELECT summary FROM session_summary WHERE session_id=?`, sessionID)
    var summary string
    if err := row.Scan(&summary); err != nil {
        if errors.Is(err, sql.ErrNoRows) { return "", nil }
        return "", err
    }
    return summary, nil
}

// UpdateSessionTitle upserts the session title.
func (s *Store) UpdateSessionTitle(sessionID, title string) error {
    // ensure row exists; reuse UpdateSessionSummary path
    _, err := s.db.Exec(`INSERT INTO session_summary(session_id, summary, updated_at, title) VALUES(?,?,?,?)
        ON CONFLICT(session_id) DO UPDATE SET title=excluded.title, updated_at=excluded.updated_at`, sessionID, "", time.Now().UTC(), title)
    return err
}

// GetSessionTitle returns the cached session title (may be empty).
func (s *Store) GetSessionTitle(sessionID string) (string, error) {
    row := s.db.QueryRow(`SELECT title FROM session_summary WHERE session_id=?`, sessionID)
    var title sql.NullString
    if err := row.Scan(&title); err != nil {
        if errors.Is(err, sql.ErrNoRows) { return "", nil }
        return "", err
    }
    if !title.Valid { return "", nil }
    return title.String, nil
}

func containsIgnoreCase(haystack, needle string) bool {
    return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// RecentDocuments returns up to N recent docs for a session (for candidate selection).
func (s *Store) RecentDocuments(sessionID string, limit int) ([]Document, error) {
    rows, err := s.db.Query(`SELECT id, speaker, start_time, end_time, original_text, summary, created_at FROM documents WHERE session_id=? ORDER BY start_time DESC LIMIT ?`, sessionID, limit)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []Document
    for rows.Next() {
        var d Document
        d.SessionID = sessionID
        var created string
        if err := rows.Scan(&d.ID, &d.Speaker, &d.StartTime, &d.EndTime, &d.Original, &d.Summary, &created); err != nil { return nil, err }
        t, _ := time.Parse(time.RFC3339Nano, created)
        d.CreatedAt = t
        out = append(out, d)
    }
    return out, nil
}

// LoadEmbeddingsForDocs loads vectors for a set of doc IDs.
func (s *Store) LoadEmbeddingsForDocs(ids []int64) (map[int64][]float32, error) {
    if len(ids) == 0 { return map[int64][]float32{}, nil }
    // Build simple IN clause
    placeholders := make([]any, 0, len(ids))
    q := "SELECT doc_id, vector_json FROM embeddings WHERE doc_id IN ("
    for i, id := range ids {
        if i > 0 { q += "," }
        q += "?"
        placeholders = append(placeholders, id)
    }
    q += ")"
    rows, err := s.db.Query(q, placeholders...)
    if err != nil { return nil, err }
    defer rows.Close()
    res := make(map[int64][]float32)
    for rows.Next() {
        var id int64
        var js string
        if err := rows.Scan(&id, &js); err != nil { return nil, err }
        var vec []float32
        if err := json.Unmarshal([]byte(js), &vec); err != nil { return nil, err }
        res[id] = vec
    }
    return res, nil
}
