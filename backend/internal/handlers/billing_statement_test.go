package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustWindow(t *testing.T, raw string, now time.Time) (time.Time, time.Time) {
	t.Helper()
	query, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("bad query %q: %v", raw, err)
	}
	from, to, err := parseStatementWindow(query, now)
	if err != nil {
		t.Fatalf("parseStatementWindow(%q): %v", raw, err)
	}
	return from, to
}

func TestParseStatementWindow(t *testing.T) {
	now := time.Date(2026, time.September, 4, 13, 45, 0, 0, time.UTC)

	from, to := mustWindow(t, "", now)
	if from.Format(time.RFC3339) != "2026-09-01T00:00:00Z" || to.Format(time.RFC3339) != "2026-10-01T00:00:00Z" {
		t.Fatalf("default window = %s..%s, want the current month", from, to)
	}

	from, to = mustWindow(t, "month=2026-02", now)
	if from.Format("2006-01-02") != "2026-02-01" || to.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("month window = %s..%s, want February", from, to)
	}

	// `to` names a day the user expects to be included, so the half-open upper
	// bound is the day after it.
	from, to = mustWindow(t, "from=2026-01-05&to=2026-01-06", now)
	if from.Format("2006-01-02") != "2026-01-05" || to.Format("2006-01-02") != "2026-01-07" {
		t.Fatalf("day window = %s..%s, want 05 through 06 inclusive", from, to)
	}

	// "everything so far" — no lower bound given.
	from, to = mustWindow(t, "to=2026-09-04", now)
	if !from.Equal(statementEpoch) || to.Format("2006-01-02") != "2026-09-05" {
		t.Fatalf("open-start window = %s..%s", from, to)
	}

	for _, bad := range []string{"month=2026-13", "month=nope", "from=2026-1-1", "to=yesterday", "from=2026-05-02&to=2026-05-01"} {
		query, err := url.ParseQuery(bad)
		if err != nil {
			t.Fatalf("bad query %q: %v", bad, err)
		}
		if _, _, err := parseStatementWindow(query, now); err == nil {
			t.Fatalf("parseStatementWindow(%q) accepted an invalid range", bad)
		}
	}
}

func TestStatementFilename(t *testing.T) {
	month := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	if got := statementFilename(month, month.AddDate(0, 1, 0)); got != "yufolo-statement-2026-09" {
		t.Fatalf("month filename = %q", got)
	}
	from := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.February, 3, 0, 0, 0, 0, time.UTC)
	if got := statementFilename(from, to); got != "yufolo-statement-2026-01-05_2026-02-02" {
		t.Fatalf("range filename = %q", got)
	}
}

func TestSetDownloadFilenameEscapesStatementName(t *testing.T) {
	recorder := httptest.NewRecorder()
	setDownloadFilename(recorder, "yufolo-statement-2026-09", "csv")
	got := recorder.Header().Get("Content-Disposition")
	if !strings.Contains(got, "yufolo-statement-2026-09.csv") {
		t.Fatalf("Content-Disposition = %q", got)
	}
}
