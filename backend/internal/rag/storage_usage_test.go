package rag

import (
	"encoding/json"
	"testing"
)

func TestEstimateIngestStorageUsageDefinesReservationAndSettlementBoundary(t *testing.T) {
	doc := &Document{
		SessionID: "session",
		Speaker:   "speaker",
		Original:  "original",
		Summary:   "summary",
	}
	vector := []float32{1, -2.5}
	encoded, err := json.Marshal(vector)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := EstimateIngestStorageUsage(doc, vector, "long summary", "short")
	if err != nil {
		t.Fatal(err)
	}
	wantDocument := int64(len("session") + len("speaker") + len("original") + len("summary"))
	if usage.DocumentBytes != wantDocument ||
		usage.EmbeddingBytes != int64(len(encoded)) ||
		usage.SummaryDeltaBytes != int64(len("short")-len("long summary")) ||
		usage.ReservationBytes != wantDocument+int64(len(encoded)) ||
		usage.NetDeltaBytes != wantDocument+int64(len(encoded))+usage.SummaryDeltaBytes {
		t.Fatalf("unexpected storage usage: %#v", usage)
	}
}
