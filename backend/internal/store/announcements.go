package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrAnnouncementInput marks a validation failure the admin console can show.
var ErrAnnouncementInput = errors.New("invalid announcement")

// Announcement is one notice shown in the workspace between starts_at and
// ends_at (nil = open-ended) while active.
type Announcement struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	LinkURL   string     `json:"link_url"`
	LinkLabel string     `json:"link_label"`
	Level     string     `json:"level"`
	Active    bool       `json:"active"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

const announcementColumns = `id, title, body, link_url, link_label, level, active, starts_at, ends_at, created_at, updated_at`

type announcementScanner interface{ Scan(...any) error }

func scanAnnouncement(row announcementScanner) (*Announcement, error) {
	a := &Announcement{}
	var endsAt sql.NullTime
	if err := row.Scan(&a.ID, &a.Title, &a.Body, &a.LinkURL, &a.LinkLabel, &a.Level, &a.Active,
		&a.StartsAt, &endsAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if endsAt.Valid {
		t := endsAt.Time
		a.EndsAt = &t
	}
	return a, nil
}

func validateAnnouncement(a *Announcement) error {
	a.Title = strings.TrimSpace(a.Title)
	a.Body = strings.TrimSpace(a.Body)
	a.LinkURL = strings.TrimSpace(a.LinkURL)
	a.LinkLabel = strings.TrimSpace(a.LinkLabel)
	a.Level = strings.ToLower(strings.TrimSpace(a.Level))
	if a.Level == "" {
		a.Level = "info"
	}
	switch {
	case a.Title == "" || utf8.RuneCountInString(a.Title) > 120:
		return fmt.Errorf("%w: title must be 1-120 characters", ErrAnnouncementInput)
	case utf8.RuneCountInString(a.Body) > 2000:
		return fmt.Errorf("%w: body must be at most 2000 characters", ErrAnnouncementInput)
	case a.Level != "info" && a.Level != "success" && a.Level != "warning":
		return fmt.Errorf("%w: level must be info, success or warning", ErrAnnouncementInput)
	case utf8.RuneCountInString(a.LinkLabel) > 60:
		return fmt.Errorf("%w: link label must be at most 60 characters", ErrAnnouncementInput)
	case len(a.LinkURL) > 500:
		return fmt.Errorf("%w: link is too long", ErrAnnouncementInput)
	}
	if a.LinkURL != "" {
		parsed, err := url.Parse(a.LinkURL)
		relative := strings.HasPrefix(a.LinkURL, "/") && !strings.HasPrefix(a.LinkURL, "//")
		if err != nil || (!relative && (parsed.Scheme != "http" && parsed.Scheme != "https")) || (!relative && parsed.Host == "") {
			return fmt.Errorf("%w: link must be an http(s) URL or a site path", ErrAnnouncementInput)
		}
	}
	if a.StartsAt.IsZero() {
		a.StartsAt = time.Now().UTC()
	}
	if a.EndsAt != nil && !a.EndsAt.After(a.StartsAt) {
		return fmt.Errorf("%w: end must be after start", ErrAnnouncementInput)
	}
	return nil
}

// ListActiveAnnouncements returns notices in their display window, newest
// first, minus the ones this user already dismissed (userID may be empty for
// guests, whose dismissals live in the browser).
func (s *PostgresStore) ListActiveAnnouncements(ctx context.Context, userID string) ([]Announcement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+announcementColumns+` FROM announcements a
		WHERE a.active AND a.starts_at <= NOW() AND (a.ends_at IS NULL OR a.ends_at > NOW())
		  AND ($1 = '' OR NOT EXISTS (
		      SELECT 1 FROM announcement_dismissals d
		      WHERE d.announcement_id = a.id AND d.user_id = CAST($1 AS UUID)))
		ORDER BY a.starts_at DESC, a.id
		LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]Announcement, 0)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	return items, rows.Err()
}

// ListAnnouncements returns every notice for the admin console, newest first.
func (s *PostgresStore) ListAnnouncements(ctx context.Context) ([]Announcement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+announcementColumns+` FROM announcements
		ORDER BY created_at DESC, id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]Announcement, 0)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	return items, rows.Err()
}

// CreateAnnouncement validates and stores a notice, filling in the generated
// fields on a.
func (s *PostgresStore) CreateAnnouncement(ctx context.Context, a *Announcement, actor string) error {
	if err := validateAnnouncement(a); err != nil {
		return err
	}
	var endsAt any
	if a.EndsAt != nil {
		endsAt = *a.EndsAt
	}
	saved, err := scanAnnouncement(s.db.QueryRowContext(ctx, `
		INSERT INTO announcements (title, body, link_url, link_label, level, active, starts_at, ends_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid)
		RETURNING `+announcementColumns,
		a.Title, a.Body, a.LinkURL, a.LinkLabel, a.Level, a.Active, a.StartsAt, endsAt, actor))
	if err != nil {
		return err
	}
	*a = *saved
	return nil
}

// UpdateAnnouncement replaces the editable fields of an existing notice.
func (s *PostgresStore) UpdateAnnouncement(ctx context.Context, id string, a *Announcement) error {
	if err := validateAnnouncement(a); err != nil {
		return err
	}
	var endsAt any
	if a.EndsAt != nil {
		endsAt = *a.EndsAt
	}
	saved, err := scanAnnouncement(s.db.QueryRowContext(ctx, `
		UPDATE announcements
		SET title = $2, body = $3, link_url = $4, link_label = $5, level = $6, active = $7,
		    starts_at = $8, ends_at = $9, updated_at = NOW()
		WHERE id = $1
		RETURNING `+announcementColumns,
		id, a.Title, a.Body, a.LinkURL, a.LinkLabel, a.Level, a.Active, a.StartsAt, endsAt))
	if err != nil {
		return err
	}
	*a = *saved
	return nil
}

// SetAnnouncementActive pauses or resumes a notice without editing it.
func (s *PostgresStore) SetAnnouncementActive(ctx context.Context, id string, active bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE announcements SET active = $2, updated_at = NOW() WHERE id = $1`, id, active)
	return requireOneRow(result, err)
}

// DeleteAnnouncement removes a notice and its dismissals.
func (s *PostgresStore) DeleteAnnouncement(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM announcements WHERE id = $1`, id)
	return requireOneRow(result, err)
}

// DismissAnnouncement records that a user closed a notice; repeating it is a
// no-op so a double click never errors.
func (s *PostgresStore) DismissAnnouncement(ctx context.Context, userID, announcementID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO announcement_dismissals (user_id, announcement_id)
		SELECT $1, id FROM announcements WHERE id = $2
		ON CONFLICT DO NOTHING`, userID, announcementID)
	return err
}
