package metrics

import "testing"

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
