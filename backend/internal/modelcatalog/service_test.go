package modelcatalog

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestModelsEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{name: "version root", base: "https://api.openai.com/v1", want: "https://api.openai.com/v1/models"},
		{name: "trailing slash", base: "https://gateway.example/v1/", want: "https://gateway.example/v1/models"},
		{name: "query removed", base: "https://gateway.example/v1?tenant=one", want: "https://gateway.example/v1/models"},
		{name: "relative rejected", base: "/v1", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := modelsEndpoint(test.base)
			if test.wantErr {
				if err == nil {
					t.Fatalf("modelsEndpoint(%q) unexpectedly succeeded: %q", test.base, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("modelsEndpoint(%q): %v", test.base, err)
			}
			if got != test.want {
				t.Fatalf("modelsEndpoint(%q) = %q, want %q", test.base, got, test.want)
			}
		})
	}
}

func TestSortAvailable(t *testing.T) {
	t.Parallel()

	models := []AvailableModel{
		{ModelID: "z-chat", Purpose: "chat"},
		{ModelID: "a-chat", Purpose: "chat", IsDefault: true},
		{ModelID: "b-summary", Purpose: "summary"},
		{ModelID: "a-summary", Purpose: "summary"},
	}
	SortAvailable(models)
	want := []AvailableModel{
		{ModelID: "a-chat", Purpose: "chat", IsDefault: true},
		{ModelID: "z-chat", Purpose: "chat"},
		{ModelID: "a-summary", Purpose: "summary"},
		{ModelID: "b-summary", Purpose: "summary"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("SortAvailable() = %#v, want %#v", models, want)
	}
}

func newCatalogTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE billing_config (
			singleton BOOLEAN PRIMARY KEY
		)`,
		`INSERT INTO billing_config (singleton) VALUES (TRUE)`,
		`CREATE TABLE provider_models (
			provider TEXT NOT NULL,
			model_id TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'provider',
			provider_available BOOLEAN NOT NULL DEFAULT TRUE,
			first_seen_at TIMESTAMP NOT NULL,
			last_seen_at TIMESTAMP NOT NULL,
			PRIMARY KEY (provider, model_id)
		)`,
		`CREATE TABLE provider_model_sync_status (
			provider TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			last_attempt_at TIMESTAMP,
			last_success_at TIMESTAMP,
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE model_policies (
			purpose TEXT NOT NULL,
			model_id TEXT NOT NULL,
			is_approved BOOLEAN NOT NULL DEFAULT FALSE,
			is_default BOOLEAN NOT NULL DEFAULT FALSE,
			cost_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
			PRIMARY KEY (purpose, model_id)
		)`,
		`CREATE TABLE provider_cost_rates (
			provider TEXT NOT NULL,
			sku TEXT NOT NULL,
			service TEXT NOT NULL,
			unit_type TEXT NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create catalog test schema: %v", err)
		}
	}
	return db
}

func TestRefreshStatusPersistsAcrossServiceRestart(t *testing.T) {
	db := newCatalogTestDB(t)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "temporary provider failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
	}))
	t.Cleanup(server.Close)

	service := &Service{
		db: db, baseURL: server.URL, apiKey: "test-key", httpClient: server.Client(),
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() success path: %v", err)
	}

	restarted := &Service{db: db}
	catalog, err := restarted.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog() after restart: %v", err)
	}
	if catalog.Status != StatusProviderConfirmed || catalog.LastSuccessAt == "" ||
		catalog.LastAttemptAt == "" || catalog.LastError != "" {
		t.Fatalf("unexpected persisted successful status: %#v", catalog)
	}
	if len(catalog.Models) != 1 ||
		catalog.Models[0].AvailabilityStatus != StatusProviderConfirmed {
		t.Fatalf("unexpected confirmed models: %#v", catalog.Models)
	}
	var successAttemptAt time.Time
	if err := db.QueryRow(`
		SELECT last_attempt_at FROM provider_model_sync_status WHERE provider = ?
	`, ProviderName).Scan(&successAttemptAt); err != nil {
		t.Fatalf("load successful last_attempt_at: %v", err)
	}

	fail.Store(true)
	time.Sleep(time.Millisecond)
	refreshErr := service.Refresh(context.Background())
	if refreshErr == nil {
		t.Fatal("Refresh() unexpectedly succeeded during provider failure")
	}
	if !errors.Is(refreshErr, ErrProviderUnavailable) {
		t.Fatalf("provider failure was not classified as unavailable: %v", refreshErr)
	}
	restartedAgain := &Service{db: db}
	catalog, err = restartedAgain.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog() after failed refresh: %v", err)
	}
	if catalog.Status != StatusTemporarilyUnavailable || catalog.LastError == "" {
		t.Fatalf("unexpected persisted failure status: %#v", catalog)
	}
	var failedAttemptAt time.Time
	if err := db.QueryRow(`
		SELECT last_attempt_at FROM provider_model_sync_status WHERE provider = ?
	`, ProviderName).Scan(&failedAttemptAt); err != nil {
		t.Fatalf("load failed last_attempt_at: %v", err)
	}
	if !failedAttemptAt.After(successAttemptAt) {
		t.Fatalf(
			"failed refresh did not advance last_attempt_at: success=%v failure=%v",
			successAttemptAt, failedAttemptAt,
		)
	}
	if len(catalog.Models) != 1 || !catalog.Models[0].ProviderAvailable ||
		catalog.Models[0].AvailabilityStatus != StatusTemporarilyUnavailable {
		t.Fatalf("transient failure destroyed last known model state: %#v", catalog.Models)
	}
}

func TestCostCompletenessIsDerivedFromActiveRates(t *testing.T) {
	db := newCatalogTestDB(t)
	now := time.Now().UTC()
	for _, row := range []struct {
		modelID       string
		costConfirmed bool
	}{
		{modelID: "stale-cost-model", costConfirmed: true},
		{modelID: "input-only-model", costConfirmed: true},
		{modelID: "actively-priced-model", costConfirmed: false},
	} {
		if _, err := db.Exec(`
			INSERT INTO provider_models
				(provider, model_id, source, provider_available, first_seen_at, last_seen_at)
			VALUES (?, ?, 'provider', TRUE, ?, ?)
		`, ProviderName, row.modelID, now, now); err != nil {
			t.Fatalf("insert provider model: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO model_policies
				(purpose, model_id, is_approved, is_default, cost_confirmed)
			VALUES ('chat', ?, TRUE, FALSE, ?)
		`, row.modelID, row.costConfirmed); err != nil {
			t.Fatalf("insert model policy: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO provider_cost_rates (provider, sku, service, unit_type, is_active)
		VALUES
		  (?, 'input-only-model', 'llm', 'input_token', TRUE),
		  (?, 'actively-priced-model', 'llm', 'input_token', TRUE),
		  (?, 'actively-priced-model', 'llm', 'output_token', TRUE)
	`, ProviderName, ProviderName, ProviderName); err != nil {
		t.Fatalf("insert active rate: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_model_sync_status
			(provider, status, last_attempt_at, last_success_at, last_error, updated_at)
		VALUES (?, ?, ?, ?, '', ?)
	`, ProviderName, StatusProviderConfirmed, now, now, now); err != nil {
		t.Fatalf("insert sync status: %v", err)
	}

	service := &Service{db: db}
	available, err := service.Available(context.Background(), "chat")
	if err != nil {
		t.Fatalf("Available(): %v", err)
	}
	if len(available) != 1 || available[0].ModelID != "actively-priced-model" {
		t.Fatalf("Available() trusted stale cost flag: %#v", available)
	}

	catalog, err := service.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog(): %v", err)
	}
	policyCosts := make(map[string]bool)
	for _, model := range catalog.Models {
		for _, policy := range model.Policies {
			policyCosts[model.ModelID] = policy.CostConfirmed
		}
	}
	if policyCosts["stale-cost-model"] || policyCosts["input-only-model"] ||
		!policyCosts["actively-priced-model"] {
		t.Fatalf("AdminCatalog() returned stale cost completeness: %#v", policyCosts)
	}
}

func TestCostCompletenessIsScopedByPurposeService(t *testing.T) {
	db := newCatalogTestDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO provider_models
			(provider, model_id, source, provider_available, first_seen_at, last_seen_at)
		VALUES (?, 'shared-sku', 'builtin', TRUE, ?, ?)
	`, ProviderName, now, now); err != nil {
		t.Fatalf("insert provider model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_policies
			(purpose, model_id, is_approved, is_default, cost_confirmed)
		VALUES
		  ('chat', 'shared-sku', TRUE, FALSE, TRUE),
		  ('embedding', 'shared-sku', TRUE, FALSE, FALSE)
	`); err != nil {
		t.Fatalf("insert model policies: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, is_active)
		VALUES (?, 'shared-sku', 'embedding', 'input_token', TRUE)
	`, ProviderName); err != nil {
		t.Fatalf("insert embedding rate: %v", err)
	}

	service := &Service{db: db}
	embeddingModels, err := service.Available(context.Background(), "embedding")
	if err != nil {
		t.Fatalf("Available(embedding): %v", err)
	}
	if len(embeddingModels) != 1 || embeddingModels[0].ModelID != "shared-sku" {
		t.Fatalf("embedding rate was not recognized: %#v", embeddingModels)
	}
	chatModels, err := service.Available(context.Background(), "chat")
	if err != nil {
		t.Fatalf("Available(chat): %v", err)
	}
	if len(chatModels) != 0 {
		t.Fatalf("embedding rate leaked into llm purpose: %#v", chatModels)
	}
	err = service.UpdatePolicy(context.Background(), PolicyUpdate{
		Purpose: "chat", ModelID: "shared-sku", IsApproved: true,
	}, "actor")
	if err == nil || !strings.Contains(err.Error(), "confirmed upstream cost") {
		t.Fatalf("UpdatePolicy(chat) accepted embedding-only cost: %v", err)
	}

	catalog, err := service.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog(): %v", err)
	}
	policyCosts := make(map[string]bool)
	for _, policy := range catalog.Models[0].Policies {
		policyCosts[policy.Purpose] = policy.CostConfirmed
	}
	if policyCosts["chat"] || !policyCosts["embedding"] {
		t.Fatalf("purpose-specific costs were mixed: %#v", policyCosts)
	}
}

func TestModelPolicyMutationRequiresBillingRevisionLock(t *testing.T) {
	db := newCatalogTestDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO provider_models
			(provider, model_id, source, provider_available, first_seen_at, last_seen_at)
		VALUES (?, 'locked-model', 'provider', TRUE, ?, ?);
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, is_active)
		VALUES
		  (?, 'locked-model', 'llm', 'input_token', TRUE),
		  (?, 'locked-model', 'llm', 'output_token', TRUE);
		DELETE FROM billing_config
	`, ProviderName, now, now, ProviderName, ProviderName); err != nil {
		t.Fatal(err)
	}

	service := &Service{db: db}
	err := service.UpdatePolicy(t.Context(), PolicyUpdate{
		Purpose: "chat", ModelID: "locked-model", IsApproved: true,
	}, "")
	if err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("policy mutation bypassed billing revision lock: %v", err)
	}
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM model_policies
		WHERE purpose = 'chat' AND model_id = 'locked-model'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("policy mutation partially committed without revision lock")
	}
}

func TestBuiltinUnverifiedIsUsableUntilProviderMarksModelUnavailable(t *testing.T) {
	db := newCatalogTestDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO provider_models
			(provider, model_id, source, provider_available, first_seen_at, last_seen_at)
		VALUES (?, 'builtin-model', 'builtin', TRUE, ?, ?)
	`, ProviderName, now, now); err != nil {
		t.Fatalf("insert provider model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_policies
			(purpose, model_id, is_approved, is_default, cost_confirmed)
		VALUES ('chat', 'builtin-model', TRUE, TRUE, TRUE)
	`); err != nil {
		t.Fatalf("insert model policy: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, is_active)
		VALUES
		  (?, 'builtin-model', 'llm', 'input_token', TRUE),
		  (?, 'builtin-model', 'llm', 'output_token', TRUE)
	`, ProviderName, ProviderName); err != nil {
		t.Fatalf("insert llm rates: %v", err)
	}

	service := &Service{db: db}
	available, err := service.Available(context.Background(), "chat")
	if err != nil {
		t.Fatalf("Available() for unverified builtin: %v", err)
	}
	if len(available) != 1 || available[0].ModelID != "builtin-model" {
		t.Fatalf("unverified builtin should remain usable: %#v", available)
	}
	catalog, err := service.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog(): %v", err)
	}
	if catalog.Models[0].AvailabilityStatus != StatusBuiltinUnverified {
		t.Fatalf("unexpected builtin status: %#v", catalog.Models[0])
	}

	if _, err := db.Exec(`
		UPDATE provider_models SET provider_available = FALSE
		WHERE provider = ? AND model_id = 'builtin-model'
	`, ProviderName); err != nil {
		t.Fatalf("mark model unavailable: %v", err)
	}
	available, err = service.Available(context.Background(), "chat")
	if err != nil {
		t.Fatalf("Available() after provider removal: %v", err)
	}
	if len(available) != 0 {
		t.Fatalf("provider-unavailable model remained usable: %#v", available)
	}
	err = service.UpdatePolicy(context.Background(), PolicyUpdate{
		Purpose: "chat", ModelID: "builtin-model", IsApproved: true,
	}, "actor")
	if err == nil || !strings.Contains(err.Error(), "not currently available") {
		t.Fatalf("UpdatePolicy() accepted unavailable model: %v", err)
	}
}

func TestConcurrentRefreshesCannotOverwriteNewerStatus(t *testing.T) {
	db := newCatalogTestDB(t)
	var requestCount atomic.Int32
	firstStarted := make(chan struct{})
	secondArrived := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch requestCount.Add(1) {
		case 1:
			close(firstStarted)
			<-releaseFirst
			http.Error(w, "older request failed late", http.StatusBadGateway)
		default:
			close(secondArrived)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"newer-model"}]}`))
		}
	}))
	t.Cleanup(server.Close)
	service := &Service{
		db: db, baseURL: server.URL, apiKey: "test-key", httpClient: server.Client(),
	}

	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { firstResult <- service.Refresh(context.Background()) }()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach provider")
	}
	go func() { secondResult <- service.Refresh(context.Background()) }()
	select {
	case <-secondArrived:
		release()
		t.Fatal("second refresh reached provider before the first completed")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-firstResult; err == nil {
		t.Fatal("older refresh unexpectedly succeeded")
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("newer refresh failed: %v", err)
	}

	catalog, err := service.AdminCatalog(context.Background())
	if err != nil {
		t.Fatalf("AdminCatalog(): %v", err)
	}
	if catalog.Status != StatusProviderConfirmed || catalog.LastError != "" ||
		len(catalog.Models) != 1 || catalog.Models[0].ModelID != "newer-model" {
		t.Fatalf("older result overwrote newer catalog state: %#v", catalog)
	}
}
