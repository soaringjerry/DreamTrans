package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/dreamtrans/backend/internal/auth"
)

// Two Speechmatics accounts back the service once the training program is
// offered: SM_API_KEY has model training enabled and SM_API_KEY_NO_TRAINING
// does not. A user's audio goes through the training account only after they
// explicitly joined the program; anonymous callers, users who declined and
// users who were never asked all use the no-training account.
//
// Without SM_API_KEY_NO_TRAINING the program is not offered and every call
// uses SM_API_KEY exactly as before.
const speechmaticsNoTrainingKeyEnv = "SM_API_KEY_NO_TRAINING"

// TrainingOptInLookup reports whether a user has joined the training
// program. Storage errors are treated as "not joined".
type TrainingOptInLookup func(ctx context.Context, userID string) (bool, error)

type speechmaticsRouting struct {
	trainingKey   string
	noTrainingKey string
	lookup        TrainingOptInLookup
}

// TrainingProgramAvailable reports whether the deployment offers the training
// program, which requires the no-training provider account.
func TrainingProgramAvailable() bool {
	return strings.TrimSpace(os.Getenv(speechmaticsNoTrainingKeyEnv)) != ""
}

func loadSpeechmaticsRouting() (*speechmaticsRouting, error) {
	trainingKey := strings.TrimSpace(os.Getenv("SM_API_KEY"))
	if trainingKey == "" {
		return nil, fmt.Errorf("SM_API_KEY environment variable not set")
	}
	return &speechmaticsRouting{
		trainingKey:   trainingKey,
		noTrainingKey: strings.TrimSpace(os.Getenv(speechmaticsNoTrainingKeyEnv)),
	}, nil
}

func (r *speechmaticsRouting) available() bool {
	return r != nil && r.noTrainingKey != ""
}

// useTrainingRoute decides which account serves this caller. Only an explicit
// opt-in by an authenticated user reaches the training account.
func (r *speechmaticsRouting) useTrainingRoute(ctx context.Context, claims *auth.UserClaims) bool {
	if !r.available() {
		return true
	}
	if claims == nil || r.lookup == nil {
		return false
	}
	optIn, err := r.lookup(ctx, claims.UserID)
	if err != nil {
		log.Printf("training opt-in lookup failed for user=%s; using no-training account: %v", claims.UserID, err)
		return false
	}
	return optIn
}

func (r *speechmaticsRouting) key(training bool) string {
	if training || !r.available() {
		return r.trainingKey
	}
	return r.noTrainingKey
}
