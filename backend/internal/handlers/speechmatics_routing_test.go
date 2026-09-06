package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
)

func TestSpeechmaticsRoutingWithoutProgrammeUsesTheOnlyKey(t *testing.T) {
	t.Setenv("SM_API_KEY", "training-key")
	t.Setenv(speechmaticsNoTrainingKeyEnv, "")
	routing, err := loadSpeechmaticsRouting()
	if err != nil {
		t.Fatal(err)
	}
	if routing.available() || TrainingProgramAvailable() {
		t.Fatal("program reported available without a no-training key")
	}
	routing.lookup = func(context.Context, string) (bool, error) { return false, nil }
	claims := &auth.UserClaims{UserID: "u1"}
	if !routing.useTrainingRoute(context.Background(), claims) || !routing.useTrainingRoute(context.Background(), nil) {
		t.Fatal("single-key deployment must keep using SM_API_KEY")
	}
	if routing.key(false) != "training-key" {
		t.Fatalf("key(false) = %q, want the only key", routing.key(false))
	}
}

func TestSpeechmaticsRoutingOnlyExplicitOptInReachesTrainingAccount(t *testing.T) {
	t.Setenv("SM_API_KEY", "training-key")
	t.Setenv(speechmaticsNoTrainingKeyEnv, "clean-key")
	routing, err := loadSpeechmaticsRouting()
	if err != nil {
		t.Fatal(err)
	}
	if !routing.available() {
		t.Fatal("program should be available")
	}
	ctx := context.Background()
	if routing.useTrainingRoute(ctx, nil) {
		t.Fatal("anonymous audio must not reach the training account")
	}
	if routing.useTrainingRoute(ctx, &auth.UserClaims{UserID: "u1"}) {
		t.Fatal("without a lookup nobody is opted in")
	}
	answers := map[string]bool{"joined": true, "declined": false}
	routing.lookup = func(_ context.Context, userID string) (bool, error) {
		if userID == "broken" {
			return true, errors.New("db down")
		}
		return answers[userID], nil
	}
	if !routing.useTrainingRoute(ctx, &auth.UserClaims{UserID: "joined"}) {
		t.Fatal("opted-in user should use the training account")
	}
	if routing.useTrainingRoute(ctx, &auth.UserClaims{UserID: "declined"}) {
		t.Fatal("declined user reached the training account")
	}
	if routing.useTrainingRoute(ctx, &auth.UserClaims{UserID: "broken"}) {
		t.Fatal("a lookup failure must fall back to the no-training account")
	}
	if routing.key(true) != "training-key" || routing.key(false) != "clean-key" {
		t.Fatalf("keys = %q / %q", routing.key(true), routing.key(false))
	}
}
