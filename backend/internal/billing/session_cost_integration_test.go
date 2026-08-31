package billing

import (
	"database/sql"
	"testing"
	"time"
)

func createIntegrationSession(t *testing.T, db *sql.DB, user integrationUser, title string) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO sessions (user_id, tenant_id, title, source_language, target_language, status)
		VALUES ($1, $2, $3, 'en', 'zh', 'completed')
		RETURNING id
	`, user.userID, user.tenantID, title).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestSessionCostSummariesIntegration verifies the per-session cost buckets
// the workspace shows: transcription and translation are summed separately
// from AI actions, refunds contribute nothing, and one user cannot read
// another user's session costs.
func TestSessionCostSummariesIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "session-cost")
	other := createIntegrationUser(t, db, "session-cost-other")
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 5, Description: "seed"}); err != nil {
		t.Fatal(err)
	}

	sessionOne := createIntegrationSession(t, db, user, "session one")
	sessionTwo := createIntegrationSession(t, db, user, "session two")
	keySuffix := time.Now().Format("150405.000000")

	// Realtime transcription: reserve 2 minutes, settle at 1 minute. The
	// settlement carries the session reference, matching the proxy.
	transcription := transcriptionMinutes(user, 2, "sc:trans:"+keySuffix)
	transcription.SessionID = &sessionOne
	if _, err := service.RecordUsage(ctx, transcription); err != nil {
		t.Fatal(err)
	}
	settlement := transcriptionMinutes(user, 1, "")
	settlement.SessionID = &sessionOne
	transcriptionUSD, err := service.SettleUsageReservation(ctx, transcription.IdempotencyKey, settlement)
	if err != nil {
		t.Fatal(err)
	}

	// A still-unsettled translation reservation counts: mid-session the map
	// must include reserved charges, not only settled ones.
	translationUSD, err := service.RecordUsage(ctx, &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, SessionID: &sessionOne, Action: "translation",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 100_000, OutputTokens: 10_000,
		IdempotencyKey: "sc:transl:" + keySuffix,
	})
	if err != nil {
		t.Fatal(err)
	}

	// AI work on the same session lands in its own bucket.
	aiUSD, err := service.RecordUsage(ctx, &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, SessionID: &sessionOne, Action: "chat",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 50_000, OutputTokens: 5_000,
		IdempotencyKey: "sc:chat:" + keySuffix,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A refunded reservation must not count toward the session's cost.
	refunded := transcriptionMinutes(user, 1, "sc:refund:"+keySuffix)
	refunded.SessionID = &sessionOne
	if _, err := service.RecordUsage(ctx, refunded); err != nil {
		t.Fatal(err)
	}
	if err := service.RefundUsage(ctx, refunded.IdempotencyKey, "provider failed"); err != nil {
		t.Fatal(err)
	}

	sessionTwoRecord := transcriptionMinutes(user, 1, "sc:two:"+keySuffix)
	sessionTwoRecord.SessionID = &sessionTwo
	sessionTwoUSD, err := service.RecordUsage(ctx, sessionTwoRecord)
	if err != nil {
		t.Fatal(err)
	}

	unknownSession := "00000000-0000-4000-8000-000000000000"
	summaries, err := service.GetSessionCostSummaries(ctx, user.userID, []string{sessionOne, sessionTwo, unknownSession})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2: %+v", len(summaries), summaries)
	}
	byID := make(map[string]SessionCostSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.SessionID] = summary
	}
	one := byID[sessionOne]
	approx(t, "session one transcription", one.TranscriptionUSD, transcriptionUSD)
	approx(t, "session one transcription seconds", one.TranscriptionSeconds, 60)
	approx(t, "session one translation", one.TranslationUSD, translationUSD)
	approx(t, "session one ai", one.AIUSD, aiUSD)
	approx(t, "session one total", one.TotalUSD, transcriptionUSD+translationUSD+aiUSD)
	two := byID[sessionTwo]
	approx(t, "session two transcription", two.TranscriptionUSD, sessionTwoUSD)
	approx(t, "session two seconds", two.TranscriptionSeconds, 60)
	approx(t, "session two total", two.TotalUSD, sessionTwoUSD)

	// Another user asking about these sessions learns nothing.
	foreign, err := service.GetSessionCostSummaries(ctx, other.userID, []string{sessionOne, sessionTwo})
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 0 {
		t.Fatalf("foreign user read %d summaries, want 0", len(foreign))
	}
}
