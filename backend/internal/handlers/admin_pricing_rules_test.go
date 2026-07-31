package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	_ "modernc.org/sqlite"
)

func TestAdminPricingRuleRoutesPropagateAuditActor(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE billing_config (
			singleton BOOLEAN PRIMARY KEY
		)`,
		`INSERT INTO billing_config (singleton) VALUES (TRUE)`,
		`CREATE TABLE pricing_rules (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			rule_type TEXT NOT NULL,
			provider TEXT,
			model TEXT,
			price_per_unit REAL NOT NULL,
			unit_type TEXT NOT NULL,
			description TEXT,
			is_active BOOLEAN NOT NULL,
			priority INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_user_id TEXT,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT,
			details BLOB NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	handler := &AdminHandler{billing: billing.NewService(db)}
	actorID := "00000000-0000-0000-0000-000000000001"
	claims := &auth.UserClaims{
		UserID: actorID,
		Role:   "super_admin",
	}
	request := func(method, target, body string) *http.Request {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		return req.WithContext(context.WithValue(
			req.Context(),
			auth.UserClaimsKey,
			claims,
		))
	}

	createResponse := httptest.NewRecorder()
	handler.HandleCreatePricingRule(
		createResponse,
		request(http.MethodPost, "/api/admin/pricing-rules", `{
			"rule_type":"translation",
			"provider":"openai-compatible",
			"model":"gpt-test",
			"price_per_unit":1,
			"unit_type":"input_token",
			"description":"created",
			"is_active":true,
			"priority":10
		}`),
	)
	if createResponse.Code != http.StatusOK {
		t.Fatalf(
			"create status=%d body=%q",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	var ruleID string
	if err := db.QueryRow("SELECT id FROM pricing_rules").Scan(&ruleID); err != nil {
		t.Fatal(err)
	}

	updateResponse := httptest.NewRecorder()
	handler.HandleUpdatePricingRule(
		updateResponse,
		request(http.MethodPatch, "/api/admin/pricing-rules/"+ruleID, `{
			"rule_type":"translation",
			"provider":"openai-compatible",
			"model":"gpt-test",
			"price_per_unit":2,
			"unit_type":"input_token",
			"description":"updated",
			"is_active":true,
			"priority":10
		}`),
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf(
			"update status=%d body=%q",
			updateResponse.Code,
			updateResponse.Body.String(),
		)
	}

	deleteResponse := httptest.NewRecorder()
	handler.HandleDeletePricingRule(
		deleteResponse,
		request(http.MethodDelete, "/api/admin/pricing-rules/"+ruleID, ""),
	)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf(
			"delete status=%d body=%q",
			deleteResponse.Code,
			deleteResponse.Body.String(),
		)
	}

	rows, err := db.Query(`
		SELECT actor_user_id, action, target_id
		FROM admin_audit_logs
		ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantActions := []string{
		"billing.pricing_rule.create",
		"billing.pricing_rule.update",
		"billing.pricing_rule.delete",
	}
	var index int
	for rows.Next() {
		var gotActor, action, targetID string
		if err := rows.Scan(&gotActor, &action, &targetID); err != nil {
			t.Fatal(err)
		}
		if index >= len(wantActions) {
			t.Fatalf("unexpected extra audit action %q", action)
		}
		if gotActor != actorID ||
			action != wantActions[index] ||
			targetID != ruleID {
			t.Fatalf(
				"audit %d = actor:%q action:%q target:%q",
				index,
				gotActor,
				action,
				targetID,
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(wantActions) {
		t.Fatalf("audit rows = %d, want %d", index, len(wantActions))
	}
}
