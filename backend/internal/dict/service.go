package dict

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strings"

    _ "modernc.org/sqlite" // sqlite driver
)

// Entry represents a dictionary entry.
type Entry struct {
    Word       string `json:"word"`
    Phonetic   string `json:"phonetic,omitempty"`
    POS        string `json:"pos,omitempty"`
    Definition string `json:"definition"`
    Extra      string `json:"extra,omitempty"`
}

// Service provides dictionary lookups from a SQLite database.
type Service struct {
    db *sql.DB
}

// Open opens (or creates) the SQLite database at path and ensures schema exists.
func Open(path string) (*Service, error) {
    db, err := sql.Open("sqlite", path)
    if err != nil { return nil, err }
    if err := initSchema(db); err != nil { _ = db.Close(); return nil, err }
    return &Service{db: db}, nil
}

// Close closes the underlying DB.
func (s *Service) Close() error { if s==nil || s.db==nil { return nil }; return s.db.Close() }

func initSchema(db *sql.DB) error {
    stmts := []string{
        `PRAGMA journal_mode=WAL;`,
        `CREATE TABLE IF NOT EXISTS dictionary (
            word TEXT PRIMARY KEY COLLATE NOCASE,
            phonetic TEXT,
            pos TEXT,
            definition TEXT NOT NULL,
            extra TEXT
        );`,
        `CREATE INDEX IF NOT EXISTS idx_dictionary_word ON dictionary(word);`,
    }
    for _, st := range stmts {
        if _, err := db.Exec(st); err != nil { return err }
    }
    return nil
}

// Lookup returns the entry for an exact word match (case-insensitive).
func (s *Service) Lookup(ctx context.Context, word string) (*Entry, error) {
    if s == nil || s.db == nil { return nil, errors.New("dictionary not loaded") }
    w := normalizeWord(word)
    row := s.db.QueryRowContext(ctx, `SELECT word, phonetic, pos, definition, extra FROM dictionary WHERE word = ?`, w)
    var e Entry
    if err := row.Scan(&e.Word, &e.Phonetic, &e.POS, &e.Definition, &e.Extra); err != nil {
        if errors.Is(err, sql.ErrNoRows) { return nil, nil }
        return nil, err
    }
    return &e, nil
}

// LookupPrefix returns up to limit entries whose word starts with prefix (case-insensitive).
func (s *Service) LookupPrefix(ctx context.Context, prefix string, limit int) ([]Entry, error) {
    if s == nil || s.db == nil { return nil, errors.New("dictionary not loaded") }
    if limit <= 0 { limit = 10 }
    p := normalizeWord(prefix)
    q := `SELECT word, phonetic, pos, definition, extra FROM dictionary WHERE word LIKE ? ORDER BY word LIMIT ?`
    rows, err := s.db.QueryContext(ctx, q, p+"%", limit)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []Entry
    for rows.Next() {
        var e Entry
        if err := rows.Scan(&e.Word, &e.Phonetic, &e.POS, &e.Definition, &e.Extra); err != nil { return nil, err }
        out = append(out, e)
    }
    return out, nil
}

// InsertOrReplace inserts or replaces a row; used by importer.
func (s *Service) InsertOrReplace(ctx context.Context, e *Entry) error {
    if s == nil || s.db == nil { return errors.New("dictionary not loaded") }
    if e == nil || strings.TrimSpace(e.Word) == "" || strings.TrimSpace(e.Definition) == "" { return fmt.Errorf("invalid entry") }
    _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO dictionary(word, phonetic, pos, definition, extra) VALUES(?,?,?,?,?)`, normalizeWord(e.Word), e.Phonetic, e.POS, e.Definition, e.Extra)
    return err
}

func normalizeWord(w string) string {
    return strings.TrimSpace(strings.ToLower(w))
}

