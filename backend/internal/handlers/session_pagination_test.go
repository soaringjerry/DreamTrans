package handlers

import (
	"math"
	"net/url"
	"strings"
	"testing"
)

func TestParseTranscriptPageParams(t *testing.T) {
	tests := []struct {
		name      string
		values    url.Values
		wantLimit int
		wantStart float64
		wantID    string
		wantAfter bool
		wantErr   bool
	}{
		{
			name:      "defaults",
			values:    url.Values{},
			wantLimit: defaultTranscriptPageSize,
		},
		{
			name: "bounded page with cursor",
			values: url.Values{
				"limit":            {"25"},
				"after_start_time": {"12.5"},
				"after_id":         {"00000000-0000-4000-8000-000000000002"},
			},
			wantLimit: 25,
			wantStart: 12.5,
			wantID:    "00000000-0000-4000-8000-000000000002",
			wantAfter: true,
		},
		{
			name:    "zero limit",
			values:  url.Values{"limit": {"0"}},
			wantErr: true,
		},
		{
			name:    "limit over maximum",
			values:  url.Values{"limit": {"1001"}},
			wantErr: true,
		},
		{
			name:    "non numeric limit",
			values:  url.Values{"limit": {"many"}},
			wantErr: true,
		},
		{
			name: "cursor start without id",
			values: url.Values{
				"after_start_time": {"1"},
			},
			wantErr: true,
		},
		{
			name: "cursor id without start",
			values: url.Values{
				"after_id": {"00000000-0000-4000-8000-000000000001"},
			},
			wantErr: true,
		},
		{
			name: "negative cursor start",
			values: url.Values{
				"after_start_time": {"-1"},
				"after_id":         {"00000000-0000-4000-8000-000000000001"},
			},
			wantErr: true,
		},
		{
			name: "invalid cursor id",
			values: url.Values{
				"after_start_time": {"1"},
				"after_id":         {"not-a-uuid"},
			},
			wantErr: true,
		},
		{
			name: "non finite cursor start",
			values: url.Values{
				"after_start_time": {"NaN"},
				"after_id":         {"00000000-0000-4000-8000-000000000001"},
			},
			wantErr: true,
		},
		{
			name: "infinite cursor start",
			values: url.Values{
				"after_start_time": {"+Inf"},
				"after_id":         {"00000000-0000-4000-8000-000000000001"},
			},
			wantErr: true,
		},
		{
			name: "oversized cursor id",
			values: url.Values{
				"after_start_time": {"1"},
				"after_id":         {strings.Repeat("x", maxTranscriptCursorIDBytes+1)},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseTranscriptPageParams(test.values)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseTranscriptPageParams() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTranscriptPageParams() error = %v", err)
			}
			if got.Limit != test.wantLimit {
				t.Fatalf("limit = %d, want %d", got.Limit, test.wantLimit)
			}
			if test.wantAfter {
				if got.After == nil {
					t.Fatal("cursor = nil, want cursor")
				}
				if math.Abs(got.After.StartTime-test.wantStart) > 1e-9 {
					t.Fatalf("cursor start = %v, want %v", got.After.StartTime, test.wantStart)
				}
				if got.After.ID != test.wantID {
					t.Fatalf("cursor id = %q, want %q", got.After.ID, test.wantID)
				}
			} else if got.After != nil {
				t.Fatalf("cursor = %#v, want nil", got.After)
			}
		})
	}
}

func TestParseIncludeTranscripts(t *testing.T) {
	tests := []struct {
		name    string
		values  url.Values
		want    bool
		wantErr bool
	}{
		{name: "default", values: url.Values{}, want: true},
		{
			name:   "explicit true",
			values: url.Values{"include_transcripts": {"true"}},
			want:   true,
		},
		{
			name:   "metadata only",
			values: url.Values{"include_transcripts": {"false"}},
			want:   false,
		},
		{
			name:    "invalid",
			values:  url.Values{"include_transcripts": {"sometimes"}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseIncludeTranscripts(test.values)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseIncludeTranscripts() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIncludeTranscripts() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("include transcripts = %v, want %v", got, test.want)
			}
		})
	}
}
