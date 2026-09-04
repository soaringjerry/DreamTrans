package billing

import (
	"testing"
	"time"
)

// TestUserStatementIntegration checks the three things a statement has to get
// right against a real database: the window is honored, the totals match the
// rows returned, and one account never sees another's records.
func TestUserStatementIntegration(t *testing.T) {
	db := integrationDB(t)
	service := newIntegrationService(t, db)
	ctx := t.Context()
	user := createIntegrationUser(t, db, "statement")
	other := createIntegrationUser(t, db, "statement-other")
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: user.userID, AmountUSD: 5, Description: "seed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdjustWallet(ctx, WalletAdjustment{UserID: other.userID, AmountUSD: 5, Description: "seed"}); err != nil {
		t.Fatal(err)
	}
	keySuffix := time.Now().Format("150405.000000")

	session := createIntegrationSession(t, db, user, "statement session")
	transcription := transcriptionMinutes(user, 3, "st:trans:"+keySuffix)
	transcription.SessionID = &session
	transcriptionUSD, err := service.RecordUsage(ctx, transcription)
	if err != nil {
		t.Fatal(err)
	}
	translationUSD, err := service.RecordUsage(ctx, &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, SessionID: &session, Action: "translation",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 100_000, OutputTokens: 10_000,
		IdempotencyKey: "st:transl:" + keySuffix,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatUSD, err := service.RecordUsage(ctx, &UsageRecord{
		UserID: user.userID, TenantID: user.tenantID, SessionID: &session, Action: "chat",
		Provider: "openai-compatible", Model: "gpt-5.6-luna", InputTokens: 50_000, OutputTokens: 5_000,
		IdempotencyKey: "st:chat:" + keySuffix,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Another account's usage must stay out of this statement.
	otherRecord := transcriptionMinutes(other, 9, "st:other:"+keySuffix)
	if _, err := service.RecordUsage(ctx, otherRecord); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	statement, err := service.UserStatement(ctx, user.userID, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(statement.Usage) != 3 {
		t.Fatalf("got %d usage rows, want 3: %+v", len(statement.Usage), statement.Usage)
	}
	approx(t, "transcription total", statement.Totals.TranscriptionUSD, transcriptionUSD)
	approx(t, "translation total", statement.Totals.TranslationUSD, translationUSD)
	approx(t, "ai total", statement.Totals.AIUSD, chatUSD)
	approx(t, "charged total", statement.Totals.ChargedUSD, transcriptionUSD+translationUSD+chatUSD)
	approx(t, "transcription seconds", statement.Totals.TranscriptionSeconds, 180)
	if statement.Truncated {
		t.Fatal("a three-row statement reported itself truncated")
	}
	if len(statement.Ledger) == 0 {
		t.Fatal("statement carried no balance movements for charged usage")
	}
	for _, entry := range statement.Ledger {
		if entry.UserID != user.userID {
			t.Fatalf("ledger row belongs to %s, want %s", entry.UserID, user.userID)
		}
	}

	// A window that closes before the charges were made must come back empty.
	empty, err := service.UserStatement(ctx, user.userID, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Usage) != 0 || len(empty.Ledger) != 0 || empty.Totals.ChargedUSD != 0 {
		t.Fatalf("past window was not empty: %+v", empty)
	}

	if _, err := service.UserStatement(ctx, user.userID, now, now); err == nil {
		t.Fatal("an empty window was accepted")
	}
}
