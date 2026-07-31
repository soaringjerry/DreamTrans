// Package models defines DreamTrans persistence and API data structures.
package models

import (
	"time"
)

// Tenant represents an organization/company in the multi-tenant system
type Tenant struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	Plan            string    `json:"plan"` // free, pro, enterprise
	APIQuotaMonthly int       `json:"api_quota_monthly"`
	StorageQuotaGB  int       `json:"storage_quota_gb"`
	MaxSessions     int       `json:"max_sessions"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// User represents a user account
type User struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	Email           string     `json:"email"`
	PasswordHash    string     `json:"-"` // Never expose password hash in JSON
	Name            string     `json:"name"`
	Role            string     `json:"role"` // user, admin, super_admin
	IsActive        bool       `json:"is_active"`
	EmailVerified   bool       `json:"email_verified"`
	Dreampoints     float64    `json:"dreampoints"`
	DreampointsUsed float64    `json:"dreampoints_used"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// UserWithTenant includes tenant information
type UserWithTenant struct {
	User
	Tenant *Tenant `json:"tenant,omitempty"`
}

// Session represents a transcription session
type Session struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	TenantID        string     `json:"tenant_id"`
	Title           string     `json:"title"`
	SourceLanguage  string     `json:"source_language"`
	TargetLanguage  string     `json:"target_language"`
	DurationSeconds int        `json:"duration_seconds"`
	Status          string     `json:"status"` // active, paused, completed, archived
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SessionWithTranscripts includes transcript data
type SessionWithTranscripts struct {
	Session
	// Keep the collection present for empty sessions. Returning an omitted or
	// null field forces every client to special-case a perfectly valid session
	// that simply contains no speech yet.
	Transcripts []Transcript `json:"transcripts"`
}

// Transcript represents a single transcript segment
type Transcript struct {
	ID                 string    `json:"id"`
	SessionID          string    `json:"session_id"`
	ClientSegmentID    string    `json:"client_segment_id"`
	TranslationGroupID *string   `json:"translation_group_id,omitempty"`
	Speaker            string    `json:"speaker"`
	Text               string    `json:"text"`
	Translation        *string   `json:"translation,omitempty"`
	StartTime          float64   `json:"start_time"`
	EndTime            *float64  `json:"end_time,omitempty"`
	Status             string    `json:"status"` // partial, confirmed, translated
	IsPartial          bool      `json:"is_partial"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AIArtifact struct {
	ID            string         `json:"id"`
	TenantID      string         `json:"tenant_id,omitempty"`
	UserID        string         `json:"user_id,omitempty"`
	SessionID     *string        `json:"session_id,omitempty"`
	ProjectID     *string        `json:"project_id,omitempty"`
	ArtifactType  string         `json:"artifact_type"`
	Title         string         `json:"title"`
	Content       string         `json:"content"`
	ContextPolicy map[string]any `json:"context_policy"`
	ContextTokens int            `json:"context_tokens"`
	Model         string         `json:"model,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type AIProject struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id,omitempty"`
	UserID           string    `json:"user_id,omitempty"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	ContextMode      string    `json:"context_mode"`
	MaxContextTokens int       `json:"max_context_tokens"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type KnowledgeSource struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	TenantID     string    `json:"tenant_id,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	SourceType   string    `json:"source_type"`
	Name         string    `json:"name"`
	MediaType    string    `json:"media_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256,omitempty"`
	BlobPath     string    `json:"-"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ChunkCount   int       `json:"chunk_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type KnowledgeChunk struct {
	ID         string    `json:"id"`
	SourceID   string    `json:"source_id"`
	ProjectID  string    `json:"project_id"`
	Ordinal    int       `json:"ordinal"`
	Content    string    `json:"content"`
	Vector     []float64 `json:"-"`
	SourceName string    `json:"source_name,omitempty"`
}

// UsageLog represents a usage record for quota tracking
type UsageLog struct {
	ID        string                 `json:"id"`
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	Action    string                 `json:"action"` // transcription, translation, rag_query, storage
	Quantity  float64                `json:"quantity"`
	SessionID *string                `json:"session_id,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	MonthKey  string                 `json:"month_key"`
}

// RefreshToken represents a JWT refresh token
type RefreshToken struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// UsageSummary represents aggregated usage for a tenant
type UsageSummary struct {
	TenantID             string  `json:"tenant_id"`
	MonthKey             string  `json:"month_key"`
	TranscriptionMinutes float64 `json:"transcription_minutes"`
	TranslationCount     int     `json:"translation_count"`
	RAGQueryCount        int     `json:"rag_query_count"`
	StorageMB            float64 `json:"storage_mb"`
	APIRequestCount      int64   `json:"api_request_count"`
}

// PlanLimits defines the limits for each subscription plan
type PlanLimits struct {
	TranscriptionMinutes int `json:"transcription_minutes"`
	RAGQueries           int `json:"rag_queries"`
	StorageGB            int `json:"storage_gb"`
	MaxSessions          int `json:"max_sessions"`
}

var PlanLimitsMap = map[string]PlanLimits{
	"free": {
		TranscriptionMinutes: 60,
		RAGQueries:           100,
		StorageGB:            1,
		MaxSessions:          10,
	},
	"pro": {
		TranscriptionMinutes: 600,
		RAGQueries:           1000,
		StorageGB:            10,
		MaxSessions:          100,
	},
	"enterprise": {
		TranscriptionMinutes: -1, // unlimited
		RAGQueries:           -1,
		StorageGB:            100,
		MaxSessions:          -1,
	},
}
