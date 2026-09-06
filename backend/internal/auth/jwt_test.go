package auth

import "testing"

func TestTokenGeneratorForKeyKeepsItsOwnAccountKey(t *testing.T) {
	training, err := NewTokenGeneratorForKey("training-key")
	if err != nil {
		t.Fatal(err)
	}
	clean, err := NewTokenGeneratorForKey("  clean-key ")
	if err != nil {
		t.Fatal(err)
	}
	if training.apiKey != "training-key" || clean.apiKey != "clean-key" {
		t.Fatalf("generators hold %q and %q", training.apiKey, clean.apiKey)
	}
	if _, err := NewTokenGeneratorForKey(" "); err == nil {
		t.Fatal("an empty account key was accepted")
	}
}
