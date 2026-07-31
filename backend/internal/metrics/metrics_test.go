package metrics

import (
	"testing"
	"time"
)

func TestSnapshotDoesNotExposeCollectorMaps(t *testing.T) {
	Reset()
	RecordChat(&Usage{Model: "model-a", PromptTokens: 1, TotalTokens: 1}, 5)

	snapshot := SnapshotMetrics()
	snapshot.Chat.PerModel["model-a"].Requests = 999
	delete(snapshot.Chat.PerModel, "model-a")

	second := SnapshotMetrics()
	if second.Chat.PerModel["model-a"] == nil {
		t.Fatal("mutating a snapshot deleted collector state")
	}
	if second.Chat.PerModel["model-a"].Requests != 1 {
		t.Fatalf("collector request count mutated through snapshot: %d", second.Chat.PerModel["model-a"].Requests)
	}
}

func TestAIIndexAndRetrievalMetrics(t *testing.T) {
	Reset()
	SetAIIndexQueueDepth(3)
	RecordAIIndex(true, 125*time.Millisecond)
	RecordAIIndex(false, 75*time.Millisecond)
	RecordRetrievalMode("hybrid")
	RecordRetrievalMode("hybrid")
	RecordRetrievalMode("lexical_fallback")
	RecordProviderDuplicateRisk()

	snapshot := SnapshotMetrics()
	if snapshot.AIIndex.QueueDepth != 3 || snapshot.AIIndex.Completed != 1 ||
		snapshot.AIIndex.Failed != 1 || snapshot.AIIndex.TotalDurationMs != 200 {
		t.Fatalf("unexpected index metrics: %#v", snapshot.AIIndex)
	}
	if snapshot.RetrievalModes["hybrid"] != 2 ||
		snapshot.RetrievalModes["lexical_fallback"] != 1 {
		t.Fatalf("unexpected retrieval metrics: %#v", snapshot.RetrievalModes)
	}
	if snapshot.ProviderDuplicateRisk != 1 {
		t.Fatalf(
			"provider duplicate risk = %d, want 1",
			snapshot.ProviderDuplicateRisk,
		)
	}

	snapshot.RetrievalModes["hybrid"] = 999
	if SnapshotMetrics().RetrievalModes["hybrid"] != 2 {
		t.Fatal("retrieval metrics map leaked through snapshot")
	}
}
