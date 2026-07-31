// Package modelcatalog manages provider discovery, global approval policy, and
// account-scoped model preferences.
package modelcatalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProviderName                 = "openai-compatible"
	refreshEvery                 = 15 * time.Minute
	maxModelsBytes               = 4 << 20
	StatusProviderConfirmed      = "provider_confirmed"
	StatusBuiltinUnverified      = "builtin_unverified"
	StatusTemporarilyUnavailable = "temporarily_unavailable"
	ModelAvailabilityUnavailable = "unavailable"
)

type ModelPolicy struct {
	Purpose       string `json:"purpose"`
	ModelID       string `json:"model_id"`
	IsApproved    bool   `json:"is_approved"`
	IsDefault     bool   `json:"is_default"`
	CostConfirmed bool   `json:"cost_confirmed"`
}

type ProviderModel struct {
	Provider           string        `json:"provider"`
	ModelID            string        `json:"model_id"`
	Source             string        `json:"source"`
	ProviderAvailable  bool          `json:"provider_available"`
	AvailabilityStatus string        `json:"availability_status"`
	FirstSeenAt        string        `json:"first_seen_at"`
	LastSeenAt         string        `json:"last_seen_at"`
	Policies           []ModelPolicy `json:"policies"`
}

type CatalogStatus struct {
	Provider       string          `json:"provider"`
	Status         string          `json:"status"`
	Models         []ProviderModel `json:"models"`
	LastSuccessAt  string          `json:"last_success_at,omitempty"`
	LastAttemptAt  string          `json:"last_attempt_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	RefreshMinutes int             `json:"refresh_minutes"`
}

type AvailableModel struct {
	ModelID   string `json:"model_id"`
	Purpose   string `json:"purpose"`
	IsDefault bool   `json:"is_default"`
}

type Preferences struct {
	TranslationModel string `json:"translation_model"`
	SummaryModel     string `json:"summary_model"`
	ChatModel        string `json:"chat_model"`
}

type PolicyUpdate struct {
	Purpose    string `json:"purpose"`
	ModelID    string `json:"model_id"`
	IsApproved bool   `json:"is_approved"`
	IsDefault  bool   `json:"is_default"`
}

type Service struct {
	db         *sql.DB
	baseURL    string
	apiKey     string
	httpClient *http.Client
	refreshMu  sync.Mutex
}

// lockBillingRevisionTx serializes model availability/policy mutations with
// billing apply/reset confirmation. Billing previews hash this state because a
// reset may revoke or select defaults based on it.
func lockBillingRevisionTx(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE billing_config
		SET singleton = singleton
		WHERE singleton = TRUE
	`)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("billing configuration singleton is missing")
	}
	return nil
}

func NewService(db *sql.DB) *Service {
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_API_BASE"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE"))
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &Service{
		db: db, baseURL: baseURL, apiKey: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = s.Refresh(refreshCtx)
		cancel()
		ticker := time.NewTicker(refreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				_ = s.Refresh(refreshCtx)
				cancel()
			}
		}
	}()
}

func modelsEndpoint(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid OpenAI-compatible base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (s *Service) Refresh(ctx context.Context) error {
	return s.refresh(ctx, "")
}

// RefreshByActor performs an administrator-requested provider refresh and
// records the successful catalog mutation in the same database transaction.
func (s *Service) RefreshByActor(ctx context.Context, actorID string) error {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return fmt.Errorf("model catalog refresh actor is required")
	}
	return s.refresh(ctx, actorID)
}

func (s *Service) refresh(ctx context.Context, actorID string) error {
	// Background and administrator-triggered refreshes share one service.
	// Serialize the complete attempt so an older, slower provider response can
	// never overwrite the catalog or status produced by a newer request.
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	attemptedAt := time.Now().UTC()
	if err := s.recordRefreshAttempt(ctx, attemptedAt); err != nil {
		return fmt.Errorf("persist model refresh attempt: %w", err)
	}
	if s.apiKey == "" {
		err := fmt.Errorf("OPENAI_API_KEY is not configured")
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	endpoint, err := modelsEndpoint(s.baseURL)
	if err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBytes+1))
	if err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	if len(body) > maxModelsBytes {
		err = fmt.Errorf("provider model response is too large")
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("provider models request returned status %d", resp.StatusCode)
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	models := make([]string, 0, len(payload.Data))
	seen := make(map[string]bool)
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > 200 || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	if len(models) == 0 {
		err = fmt.Errorf("provider returned no valid models")
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.recordRefreshError(attemptedAt, err)
		return err
	}
	defer func() { _ = tx.Rollback() }()
	failTransaction := func(refreshErr error) error {
		_ = tx.Rollback()
		s.recordRefreshError(attemptedAt, refreshErr)
		return refreshErr
	}
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return failTransaction(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE provider_models
		SET provider_available = FALSE
		WHERE provider = $1
	`, ProviderName); err != nil {
		return failTransaction(err)
	}
	refreshedAt := time.Now().UTC()
	for _, id := range models {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_models
				(provider, model_id, source, provider_available, first_seen_at, last_seen_at)
			VALUES ($1, $2, 'provider', TRUE, $3, $3)
			ON CONFLICT (provider, model_id) DO UPDATE SET
				source = CASE
					WHEN provider_models.source IN ('builtin', 'builtin+provider')
						THEN 'builtin+provider'
					ELSE 'provider'
				END,
				provider_available = TRUE,
				last_seen_at = EXCLUDED.last_seen_at
		`, ProviderName, id, refreshedAt); err != nil {
			return failTransaction(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_model_sync_status
			(provider, status, last_attempt_at, last_success_at, last_error, updated_at)
		VALUES ($1, $2, $3, $4, '', $4)
		ON CONFLICT (provider) DO UPDATE SET
			status = EXCLUDED.status,
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_success_at = EXCLUDED.last_success_at,
			last_error = '',
			updated_at = EXCLUDED.updated_at
	`, ProviderName, StatusProviderConfirmed, attemptedAt, refreshedAt); err != nil {
		return failTransaction(err)
	}
	if actorID != "" {
		details, marshalErr := json.Marshal(map[string]any{
			"provider":    ProviderName,
			"model_count": len(models),
			"status":      StatusProviderConfirmed,
		})
		if marshalErr != nil {
			return failTransaction(marshalErr)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_audit_logs
				(actor_user_id, action, target_type, target_id, details)
			VALUES ($1, 'model.catalog.refresh', 'model_catalog', $2, $3)
		`, actorID, ProviderName, details); err != nil {
			return failTransaction(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return failTransaction(err)
	}
	return nil
}

func (s *Service) recordRefreshAttempt(ctx context.Context, attemptedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_model_sync_status
			(provider, status, last_attempt_at, last_error, updated_at)
		VALUES ($1, $2, $3, '', $3)
		ON CONFLICT (provider) DO UPDATE SET
			last_attempt_at = EXCLUDED.last_attempt_at,
			updated_at = EXCLUDED.updated_at
	`, ProviderName, StatusBuiltinUnverified, attemptedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) recordRefreshError(attemptedAt time.Time, refreshErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failedAt := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("failed to start provider model refresh error transaction: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		log.Printf("failed to lock provider model refresh error state: %v", err)
		return
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_model_sync_status
			(provider, status, last_attempt_at, last_error, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider) DO UPDATE SET
			status = EXCLUDED.status,
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at
	`, ProviderName, StatusTemporarilyUnavailable, attemptedAt, refreshErr.Error(), failedAt); err != nil {
		log.Printf("failed to persist provider model refresh error: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("failed to commit provider model refresh error: %v", err)
	}
}

type providerSyncState struct {
	Status        string
	LastAttemptAt sql.NullTime
	LastSuccessAt sql.NullTime
	LastError     string
}

func (s *Service) providerSyncState(ctx context.Context) (providerSyncState, error) {
	state := providerSyncState{Status: StatusBuiltinUnverified}
	err := s.db.QueryRowContext(ctx, `
		SELECT status, last_attempt_at, last_success_at, last_error
		FROM provider_model_sync_status
		WHERE provider = $1
	`, ProviderName).Scan(
		&state.Status, &state.LastAttemptAt, &state.LastSuccessAt, &state.LastError,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	return state, err
}

func modelAvailabilityStatus(model *ProviderModel, providerStatus string) string {
	if !model.ProviderAvailable {
		return ModelAvailabilityUnavailable
	}
	if providerStatus == StatusTemporarilyUnavailable {
		return StatusTemporarilyUnavailable
	}
	if providerStatus == StatusProviderConfirmed &&
		(model.Source == "provider" || model.Source == "builtin+provider") {
		return StatusProviderConfirmed
	}
	return StatusBuiltinUnverified
}

func (s *Service) AdminCatalog(ctx context.Context) (*CatalogStatus, error) {
	syncState, err := s.providerSyncState(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, model_id, source, provider_available, first_seen_at, last_seen_at
		FROM provider_models
		ORDER BY model_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	models := make([]ProviderModel, 0)
	index := make(map[string]int)
	for rows.Next() {
		var model ProviderModel
		if err := rows.Scan(&model.Provider, &model.ModelID, &model.Source,
			&model.ProviderAvailable, &model.FirstSeenAt, &model.LastSeenAt); err != nil {
			return nil, err
		}
		index[model.ModelID] = len(models)
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	policyRows, err := s.db.QueryContext(ctx, `
		SELECT purpose, model_id, is_approved, is_default,
		       EXISTS (
		         SELECT 1 FROM provider_cost_rates costs
		         WHERE costs.provider = $1
		           AND costs.sku = model_policies.model_id
		           AND costs.service = CASE
		             WHEN model_policies.purpose = 'embedding' THEN 'embedding'
		             ELSE 'llm'
		           END
		           AND costs.unit_type = 'input_token'
		           AND costs.is_active = TRUE
		       ) AND (
		         model_policies.purpose = 'embedding'
		         OR EXISTS (
		           SELECT 1 FROM provider_cost_rates costs
		           WHERE costs.provider = $1
		             AND costs.sku = model_policies.model_id
		             AND costs.service = CASE
		               WHEN model_policies.purpose = 'embedding' THEN 'embedding'
		               ELSE 'llm'
		             END
		             AND costs.unit_type = 'output_token'
		             AND costs.is_active = TRUE
		         )
		       )
		FROM model_policies
		ORDER BY purpose, model_id
	`, ProviderName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = policyRows.Close() }()
	for policyRows.Next() {
		var policy ModelPolicy
		if err := policyRows.Scan(&policy.Purpose, &policy.ModelID, &policy.IsApproved,
			&policy.IsDefault, &policy.CostConfirmed); err != nil {
			return nil, err
		}
		if position, ok := index[policy.ModelID]; ok {
			models[position].Policies = append(models[position].Policies, policy)
		}
	}
	if err := policyRows.Err(); err != nil {
		return nil, err
	}
	costRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT service, sku, unit_type
		FROM provider_cost_rates
		WHERE provider = $1 AND is_active = TRUE
	`, ProviderName)
	if err != nil {
		return nil, err
	}
	costUnits := make(map[string]map[string]bool)
	for costRows.Next() {
		var service, modelID, unitType string
		if err := costRows.Scan(&service, &modelID, &unitType); err != nil {
			_ = costRows.Close()
			return nil, err
		}
		key := service + "\x00" + modelID
		if costUnits[key] == nil {
			costUnits[key] = make(map[string]bool)
		}
		costUnits[key][unitType] = true
	}
	if err := costRows.Close(); err != nil {
		return nil, err
	}
	for i := range models {
		purposes := []string{"translation", "summary", "chat"}
		if strings.HasPrefix(models[i].ModelID, "text-embedding-") {
			purposes = []string{"embedding"}
		}
		for _, purpose := range purposes {
			found := false
			for _, policy := range models[i].Policies {
				if policy.Purpose == purpose {
					found = true
					break
				}
			}
			if !found {
				service := costServiceForPurpose(purpose)
				models[i].Policies = append(models[i].Policies, ModelPolicy{
					Purpose: purpose, ModelID: models[i].ModelID,
					CostConfirmed: costCompleteForPurpose(
						costUnits[service+"\x00"+models[i].ModelID], purpose,
					),
				})
			}
		}
		models[i].AvailabilityStatus = modelAvailabilityStatus(&models[i], syncState.Status)
	}
	status := &CatalogStatus{
		Provider: ProviderName, Status: syncState.Status, Models: models,
		LastError:      syncState.LastError,
		RefreshMinutes: int(refreshEvery / time.Minute),
	}
	if syncState.LastSuccessAt.Valid {
		status.LastSuccessAt = syncState.LastSuccessAt.Time.UTC().Format(time.RFC3339)
	}
	if syncState.LastAttemptAt.Valid {
		status.LastAttemptAt = syncState.LastAttemptAt.Time.UTC().Format(time.RFC3339)
	}
	return status, nil
}

func validPurpose(purpose string) bool {
	return purpose == "translation" || purpose == "summary" ||
		purpose == "chat" || purpose == "embedding"
}

func costServiceForPurpose(purpose string) string {
	if purpose == "embedding" {
		return "embedding"
	}
	if validPurpose(purpose) {
		return "llm"
	}
	return ""
}

func costCompleteForPurpose(units map[string]bool, purpose string) bool {
	if !units["input_token"] {
		return false
	}
	return purpose == "embedding" || units["output_token"]
}

func (s *Service) UpdatePolicy(ctx context.Context, update PolicyUpdate, actorID string) error {
	update.Purpose = strings.TrimSpace(update.Purpose)
	update.ModelID = strings.TrimSpace(update.ModelID)
	if !validPurpose(update.Purpose) || update.ModelID == "" || len(update.ModelID) > 200 {
		return fmt.Errorf("invalid model policy")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}
	var exists, providerAvailable, costConfirmed bool
	costService := costServiceForPurpose(update.Purpose)
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM provider_models
		  WHERE provider = $1 AND model_id = $2
		), COALESCE((
		  SELECT provider_available FROM provider_models
		  WHERE provider = $1 AND model_id = $2
		), FALSE
		), EXISTS (
		  SELECT 1 FROM provider_cost_rates
		  WHERE provider = $1 AND sku = $2
		    AND service = $4
		    AND unit_type = 'input_token' AND is_active = TRUE
		) AND (
		  $3 = 'embedding' OR EXISTS (
		    SELECT 1 FROM provider_cost_rates
		    WHERE provider = $1 AND sku = $2
		      AND service = $4
		      AND unit_type = 'output_token' AND is_active = TRUE
		  )
		)
	`, ProviderName, update.ModelID, update.Purpose, costService).Scan(
		&exists, &providerAvailable, &costConfirmed,
	); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("model is not in the provider catalog")
	}
	if update.IsApproved && !providerAvailable {
		return fmt.Errorf("model is not currently available from the provider")
	}
	if update.IsApproved && !costConfirmed {
		return fmt.Errorf("model requires a confirmed upstream cost before approval")
	}
	if update.IsDefault && !update.IsApproved {
		return fmt.Errorf("default model must be approved")
	}
	if update.IsDefault {
		if _, err := tx.ExecContext(ctx, `
			UPDATE model_policies
			SET is_default = FALSE, updated_at = NOW(), updated_by = $1
			WHERE purpose = $2
		`, actorID, update.Purpose); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_policies
			(purpose, model_id, is_approved, is_default, cost_confirmed, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (purpose, model_id) DO UPDATE SET
			is_approved = EXCLUDED.is_approved,
			is_default = EXCLUDED.is_default,
			cost_confirmed = EXCLUDED.cost_confirmed,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, update.Purpose, update.ModelID, update.IsApproved, update.IsDefault,
		costConfirmed, actorID); err != nil {
		return err
	}
	details, _ := json.Marshal(update)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_audit_logs
			(actor_user_id, action, target_type, target_id, details)
		VALUES ($1, 'model.policy.update', 'model', $2, $3)
	`, actorID, update.ModelID, details); err != nil {
		return err
	}
	return tx.Commit()
}

var builtinDefaultPolicies = []struct {
	Purpose string
	ModelID string
}{
	{Purpose: "translation", ModelID: "gpt-5.6-luna"},
	{Purpose: "summary", ModelID: "gpt-5.6-sol"},
	{Purpose: "chat", ModelID: "gpt-5.6-sol"},
	{Purpose: "embedding", ModelID: "text-embedding-3-small"},
}

// ReconcilePoliciesAfterBillingReset derives cost completeness from active
// rates, revokes unsafe policies, and restores one priced built-in default for
// every purpose. Billing reset callers should invoke this after restoring the
// built-in cost catalog.
func (s *Service) ReconcilePoliciesAfterBillingReset(ctx context.Context, actorID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE model_policies policies
		SET cost_confirmed = EXISTS (
		      SELECT 1 FROM provider_cost_rates costs
		      WHERE costs.provider = $1
		        AND costs.sku = policies.model_id
		        AND costs.service = CASE
		          WHEN policies.purpose = 'embedding' THEN 'embedding'
		          ELSE 'llm'
		        END
		        AND costs.unit_type = 'input_token'
		        AND costs.is_active = TRUE
		    ) AND (
		      policies.purpose = 'embedding' OR EXISTS (
		        SELECT 1 FROM provider_cost_rates costs
		        WHERE costs.provider = $1
		          AND costs.sku = policies.model_id
		          AND costs.service = CASE
		            WHEN policies.purpose = 'embedding' THEN 'embedding'
		            ELSE 'llm'
		          END
		          AND costs.unit_type = 'output_token'
		          AND costs.is_active = TRUE
		      )
		    ),
		    is_approved = CASE
		      WHEN EXISTS (
		        SELECT 1 FROM provider_cost_rates costs
		        WHERE costs.provider = $1
		          AND costs.sku = policies.model_id
		          AND costs.service = CASE
		            WHEN policies.purpose = 'embedding' THEN 'embedding'
		            ELSE 'llm'
		          END
		          AND costs.unit_type = 'input_token'
		          AND costs.is_active = TRUE
		      ) AND (
		        policies.purpose = 'embedding' OR EXISTS (
		          SELECT 1 FROM provider_cost_rates costs
		          WHERE costs.provider = $1
		            AND costs.sku = policies.model_id
		            AND costs.service = CASE
		              WHEN policies.purpose = 'embedding' THEN 'embedding'
		              ELSE 'llm'
		            END
		            AND costs.unit_type = 'output_token'
		            AND costs.is_active = TRUE
		        )
		      ) AND EXISTS (
		        SELECT 1 FROM provider_models models
		        WHERE models.provider = $1
		          AND models.model_id = policies.model_id
		          AND models.provider_available = TRUE
		      ) THEN policies.is_approved
		      ELSE FALSE
		    END,
		    is_default = CASE
		      WHEN EXISTS (
		        SELECT 1 FROM provider_cost_rates costs
		        WHERE costs.provider = $1
		          AND costs.sku = policies.model_id
		          AND costs.service = CASE
		            WHEN policies.purpose = 'embedding' THEN 'embedding'
		            ELSE 'llm'
		          END
		          AND costs.unit_type = 'input_token'
		          AND costs.is_active = TRUE
		      ) AND (
		        policies.purpose = 'embedding' OR EXISTS (
		          SELECT 1 FROM provider_cost_rates costs
		          WHERE costs.provider = $1
		            AND costs.sku = policies.model_id
		            AND costs.service = CASE
		              WHEN policies.purpose = 'embedding' THEN 'embedding'
		              ELSE 'llm'
		            END
		            AND costs.unit_type = 'output_token'
		            AND costs.is_active = TRUE
		        )
		      ) AND EXISTS (
		        SELECT 1 FROM provider_models models
		        WHERE models.provider = $1
		          AND models.model_id = policies.model_id
		          AND models.provider_available = TRUE
		      ) THEN policies.is_default
		      ELSE FALSE
		    END,
		    updated_at = NOW(),
		    updated_by = $2
	`, ProviderName, actorID); err != nil {
		return err
	}

	for _, policy := range builtinDefaultPolicies {
		var costComplete bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM provider_cost_rates
			  WHERE provider = $1 AND sku = $2
			    AND service = $4
			    AND unit_type = 'input_token' AND is_active = TRUE
			) AND (
			  $3 = 'embedding' OR EXISTS (
			    SELECT 1 FROM provider_cost_rates
			    WHERE provider = $1 AND sku = $2
			      AND service = $4
			      AND unit_type = 'output_token' AND is_active = TRUE
			  )
			)
		`, ProviderName, policy.ModelID, policy.Purpose,
			costServiceForPurpose(policy.Purpose)).Scan(&costComplete); err != nil {
			return err
		}
		if !costComplete {
			return fmt.Errorf(
				"built-in default %s model %s has no active cost",
				policy.Purpose, policy.ModelID,
			)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_models
				(provider, model_id, source, provider_available)
			VALUES ($1, $2, 'builtin', TRUE)
			ON CONFLICT (provider, model_id) DO NOTHING
		`, ProviderName, policy.ModelID); err != nil {
			return err
		}
		var providerAvailable bool
		if err := tx.QueryRowContext(ctx, `
			SELECT provider_available
			FROM provider_models
			WHERE provider = $1 AND model_id = $2
		`, ProviderName, policy.ModelID).Scan(&providerAvailable); err != nil {
			return err
		}
		if !providerAvailable {
			return fmt.Errorf(
				"built-in default %s model %s is unavailable from the provider",
				policy.Purpose, policy.ModelID,
			)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE model_policies
			SET is_default = FALSE, updated_at = NOW(), updated_by = $1
			WHERE purpose = $2 AND model_id <> $3
		`, actorID, policy.Purpose, policy.ModelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO model_policies
				(purpose, model_id, is_approved, is_default, cost_confirmed, updated_by)
			VALUES ($1, $2, TRUE, TRUE, TRUE, $3)
			ON CONFLICT (purpose, model_id) DO UPDATE SET
				is_approved = TRUE,
				is_default = TRUE,
				cost_confirmed = TRUE,
				updated_by = EXCLUDED.updated_by,
				updated_at = NOW()
		`, policy.Purpose, policy.ModelID, actorID); err != nil {
			return err
		}
	}

	details, err := json.Marshal(map[string]any{
		"provider": ProviderName,
		"defaults": builtinDefaultPolicies,
	})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_audit_logs
			(actor_user_id, action, target_type, target_id, details)
		VALUES ($1, 'model.policies.reconcile', 'model_catalog', $2, $3)
	`, actorID, ProviderName, details); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Available(ctx context.Context, purpose string) ([]AvailableModel, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose != "" && !validPurpose(purpose) {
		return nil, fmt.Errorf("invalid model purpose")
	}
	args := []any{}
	filter := ""
	if purpose != "" {
		args = append(args, purpose)
		filter = " AND policies.purpose = $2"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT policies.model_id, policies.purpose, policies.is_default
		FROM model_policies policies
		JOIN provider_models models
		  ON models.provider = $1 AND models.model_id = policies.model_id
		WHERE policies.is_approved = TRUE
		  AND EXISTS (
		    SELECT 1 FROM provider_cost_rates costs
		    WHERE costs.provider = $1
		      AND costs.sku = policies.model_id
		      AND costs.service = CASE
		        WHEN policies.purpose = 'embedding' THEN 'embedding'
		        ELSE 'llm'
		      END
		      AND costs.unit_type = 'input_token'
		      AND costs.is_active = TRUE
		  )
		  AND (
		    policies.purpose = 'embedding' OR EXISTS (
		      SELECT 1 FROM provider_cost_rates costs
		      WHERE costs.provider = $1
		        AND costs.sku = policies.model_id
		        AND costs.service = CASE
		          WHEN policies.purpose = 'embedding' THEN 'embedding'
		          ELSE 'llm'
		        END
		        AND costs.unit_type = 'output_token'
		        AND costs.is_active = TRUE
		    )
		  )
		  AND models.provider_available = TRUE`+filter+`
		ORDER BY policies.purpose, policies.is_default DESC, policies.model_id
	`, append([]any{ProviderName}, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []AvailableModel
	for rows.Next() {
		var item AvailableModel
		if err := rows.Scan(&item.ModelID, &item.Purpose, &item.IsDefault); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) effectiveModel(ctx context.Context, userID, purpose string) (string, error) {
	column := map[string]string{
		"translation": "translation_model",
		"summary":     "summary_model",
		"chat":        "chat_model",
	}[purpose]
	if column == "" {
		return "", fmt.Errorf("unsupported user-selectable purpose")
	}
	var preferred sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT `+column+` FROM user_model_preferences WHERE user_id = $1
	`, userID).Scan(&preferred)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if preferred.Valid {
		var allowed bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM model_policies policies
			  JOIN provider_models models
			    ON models.provider = $1 AND models.model_id = policies.model_id
			  WHERE policies.purpose = $2 AND policies.model_id = $3
			    AND policies.is_approved = TRUE
			    AND EXISTS (
			      SELECT 1 FROM provider_cost_rates costs
			      WHERE costs.provider = $1
			        AND costs.sku = policies.model_id
			        AND costs.service = CASE
			          WHEN policies.purpose = 'embedding' THEN 'embedding'
			          ELSE 'llm'
			        END
			        AND costs.unit_type = 'input_token'
			        AND costs.is_active = TRUE
			    )
			    AND (
			      policies.purpose = 'embedding' OR EXISTS (
			        SELECT 1 FROM provider_cost_rates costs
			        WHERE costs.provider = $1
			          AND costs.sku = policies.model_id
			          AND costs.service = CASE
			            WHEN policies.purpose = 'embedding' THEN 'embedding'
			            ELSE 'llm'
			          END
			          AND costs.unit_type = 'output_token'
			          AND costs.is_active = TRUE
			      )
			    )
			    AND models.provider_available = TRUE
			)
		`, ProviderName, purpose, preferred.String).Scan(&allowed); err != nil {
			return "", err
		}
		if allowed {
			return preferred.String, nil
		}
	}
	var fallback string
	err = s.db.QueryRowContext(ctx, `
		SELECT policies.model_id
		FROM model_policies policies
		JOIN provider_models models
		  ON models.provider = $1 AND models.model_id = policies.model_id
		WHERE policies.purpose = $2
		  AND policies.is_approved = TRUE
		  AND EXISTS (
		    SELECT 1 FROM provider_cost_rates costs
		    WHERE costs.provider = $1
		      AND costs.sku = policies.model_id
		      AND costs.service = CASE
		        WHEN policies.purpose = 'embedding' THEN 'embedding'
		        ELSE 'llm'
		      END
		      AND costs.unit_type = 'input_token'
		      AND costs.is_active = TRUE
		  )
		  AND (
		    policies.purpose = 'embedding' OR EXISTS (
		      SELECT 1 FROM provider_cost_rates costs
		      WHERE costs.provider = $1
		        AND costs.sku = policies.model_id
		        AND costs.service = CASE
		          WHEN policies.purpose = 'embedding' THEN 'embedding'
		          ELSE 'llm'
		        END
		        AND costs.unit_type = 'output_token'
		        AND costs.is_active = TRUE
		    )
		  )
		  AND models.provider_available = TRUE
		ORDER BY policies.is_default DESC, policies.model_id
		LIMIT 1
	`, ProviderName, purpose).Scan(&fallback)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no approved %s model is available", purpose)
	}
	return fallback, err
}

func (s *Service) EffectivePreferences(ctx context.Context, userID string) (Preferences, error) {
	var prefs Preferences
	var err error
	if prefs.TranslationModel, err = s.effectiveModel(ctx, userID, "translation"); err != nil {
		return prefs, err
	}
	if prefs.SummaryModel, err = s.effectiveModel(ctx, userID, "summary"); err != nil {
		return prefs, err
	}
	if prefs.ChatModel, err = s.effectiveModel(ctx, userID, "chat"); err != nil {
		return prefs, err
	}
	return prefs, nil
}

func (s *Service) IsAllowed(ctx context.Context, purpose, modelID string) (bool, error) {
	purpose = strings.TrimSpace(purpose)
	modelID = strings.TrimSpace(modelID)
	if !validPurpose(purpose) || modelID == "" {
		return false, nil
	}
	var allowed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM model_policies policies
		  JOIN provider_models models
		    ON models.provider = $1 AND models.model_id = policies.model_id
		  WHERE policies.purpose = $2 AND policies.model_id = $3
		    AND policies.is_approved = TRUE
		    AND EXISTS (
		      SELECT 1 FROM provider_cost_rates costs
		      WHERE costs.provider = $1
		        AND costs.sku = policies.model_id
		        AND costs.service = CASE
		          WHEN policies.purpose = 'embedding' THEN 'embedding'
		          ELSE 'llm'
		        END
		        AND costs.unit_type = 'input_token'
		        AND costs.is_active = TRUE
		    )
		    AND (
		      policies.purpose = 'embedding' OR EXISTS (
		        SELECT 1 FROM provider_cost_rates costs
		        WHERE costs.provider = $1
		          AND costs.sku = policies.model_id
		          AND costs.service = CASE
		            WHEN policies.purpose = 'embedding' THEN 'embedding'
		            ELSE 'llm'
		          END
		          AND costs.unit_type = 'output_token'
		          AND costs.is_active = TRUE
		      )
		    )
		    AND models.provider_available = TRUE
		)
	`, ProviderName, purpose, modelID).Scan(&allowed)
	return allowed, err
}

func (s *Service) SavePreferences(ctx context.Context, userID string, prefs Preferences) (Preferences, error) {
	requested := map[string]string{
		"translation": strings.TrimSpace(prefs.TranslationModel),
		"summary":     strings.TrimSpace(prefs.SummaryModel),
		"chat":        strings.TrimSpace(prefs.ChatModel),
	}
	for purpose, model := range requested {
		if model == "" {
			continue
		}
		var allowed bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM model_policies policies
			  JOIN provider_models models
			    ON models.provider = $1 AND models.model_id = policies.model_id
			  WHERE policies.purpose = $2 AND policies.model_id = $3
			    AND policies.is_approved = TRUE
			    AND EXISTS (
			      SELECT 1 FROM provider_cost_rates costs
			      WHERE costs.provider = $1
			        AND costs.sku = policies.model_id
			        AND costs.service = CASE
			          WHEN policies.purpose = 'embedding' THEN 'embedding'
			          ELSE 'llm'
			        END
			        AND costs.unit_type = 'input_token'
			        AND costs.is_active = TRUE
			    )
			    AND (
			      policies.purpose = 'embedding' OR EXISTS (
			        SELECT 1 FROM provider_cost_rates costs
			        WHERE costs.provider = $1
			          AND costs.sku = policies.model_id
			          AND costs.service = CASE
			            WHEN policies.purpose = 'embedding' THEN 'embedding'
			            ELSE 'llm'
			          END
			          AND costs.unit_type = 'output_token'
			          AND costs.is_active = TRUE
			      )
			    )
			    AND models.provider_available = TRUE
			)
		`, ProviderName, purpose, model).Scan(&allowed); err != nil {
			return Preferences{}, err
		}
		if !allowed {
			return Preferences{}, fmt.Errorf("%s model is not approved or priced", purpose)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO user_model_preferences
			(user_id, translation_model, summary_model, chat_model, updated_at)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			translation_model = EXCLUDED.translation_model,
			summary_model = EXCLUDED.summary_model,
			chat_model = EXCLUDED.chat_model,
			updated_at = NOW()
	`, userID, requested["translation"], requested["summary"], requested["chat"]); err != nil {
		return Preferences{}, err
	}
	return s.EffectivePreferences(ctx, userID)
}

func SortAvailable(models []AvailableModel) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Purpose == models[j].Purpose {
			if models[i].IsDefault != models[j].IsDefault {
				return models[i].IsDefault
			}
			return models[i].ModelID < models[j].ModelID
		}
		return models[i].Purpose < models[j].Purpose
	})
}
