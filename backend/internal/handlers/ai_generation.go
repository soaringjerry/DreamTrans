package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

const aiGenerationLease = 3 * time.Minute

var (
	errAIGenerationInProgress = errors.New("AI generation request is already in progress")
	errAIGenerationConflict   = errors.New("client_request_id was already used for another AI request")
)

type aiGenerationClaim struct {
	request models.AIGenerationRequest
}

func hashAIGenerationPayload(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (h *RAGHandler) beginAIGeneration(
	ctx context.Context,
	clientRequestID string,
	requestKind string,
	requestHash string,
	sessionID string,
) (*aiGenerationClaim, json.RawMessage, error) {
	claims := auth.GetUserClaims(ctx)
	if clientRequestID == "" || claims == nil || h.store == nil {
		return nil, nil, nil
	}
	request := models.AIGenerationRequest{
		TenantID: claims.TenantID, UserID: claims.UserID,
		ClientRequestID: clientRequestID, RequestKind: requestKind,
		RequestHash: requestHash, LeaseOwner: "ai-generation-" + uuid.NewString(),
	}
	if sessionID = strings.TrimSpace(sessionID); uuid.Validate(sessionID) == nil {
		request.SessionID = &sessionID
	}
	result, err := h.store.BeginAIGenerationRequest(
		ctx, &request, aiGenerationLease,
	)
	if err != nil {
		return nil, nil, err
	}
	switch result.Outcome {
	case models.AIGenerationOutcomeAcquired:
		return &aiGenerationClaim{request: result.Request}, nil, nil
	case models.AIGenerationOutcomeReplay:
		return nil, append(json.RawMessage(nil), result.Request.ResponseJSON...), nil
	case models.AIGenerationOutcomeInProgress:
		return nil, nil, errAIGenerationInProgress
	case models.AIGenerationOutcomeHashConflict:
		return nil, nil, errAIGenerationConflict
	default:
		return nil, nil, fmt.Errorf("unsupported AI generation reservation outcome")
	}
}

func (h *RAGHandler) releaseAIGeneration(
	ctx context.Context,
	claim *aiGenerationClaim,
) error {
	if claim == nil {
		return nil
	}
	return h.store.ReleaseAIGenerationRequest(
		ctx,
		claim.request.ID,
		claim.request.TenantID,
		claim.request.UserID,
		claim.request.LeaseOwner,
	)
}

func (h *RAGHandler) completeAIGeneration(
	ctx context.Context,
	claim *aiGenerationClaim,
	response any,
) error {
	if claim == nil {
		return nil
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return h.store.CompleteAIGenerationRequest(
		ctx,
		claim.request.ID,
		claim.request.TenantID,
		claim.request.UserID,
		claim.request.LeaseOwner,
		encoded,
	)
}

func (h *RAGHandler) failAIGeneration(claim *aiGenerationClaim, reason string) {
	if claim == nil || h.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.store.FailAIGenerationRequest(
		ctx,
		claim.request.ID,
		claim.request.TenantID,
		claim.request.UserID,
		claim.request.LeaseOwner,
		safeIndexError(errors.New(reason)),
	)
}

func writeAIGenerationReplay(w http.ResponseWriter, response json.RawMessage) error {
	if len(response) == 0 || !json.Valid(response) {
		return errors.New("stored AI response is invalid")
	}
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(response)
	return err
}

func writeAIGenerationBeginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAIGenerationInProgress):
		w.Header().Set("Retry-After", "2")
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errAIGenerationConflict),
		errors.Is(err, store.ErrIdempotencyConflict):
		http.Error(w, errAIGenerationConflict.Error(), http.StatusConflict)
	default:
		http.Error(w, "failed to reserve AI generation request", http.StatusInternalServerError)
	}
}
