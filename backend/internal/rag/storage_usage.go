package rag

import (
	"encoding/json"
	"fmt"
)

// IngestStorageUsage is the storage-engine-independent logical payload delta
// known immediately before an ingest's first persistent write. It deliberately
// excludes SQLite/Postgres page, index, and WAL overhead so one tenant quota has
// stable semantics across storage engines.
type IngestStorageUsage struct {
	DocumentBytes     int64
	EmbeddingBytes    int64
	SummaryDeltaBytes int64
	// ReservationBytes is the non-negative upper bound to reserve before write.
	ReservationBytes int64
	// NetDeltaBytes is the logical usage change after replacing the old summary.
	NetDeltaBytes int64
}

// EstimateIngestStorageUsage measures exactly the variable payload represented
// by the service operation: UTF-8 document fields, the serialized embedding,
// and the replacement delta for session_summary. Callers can reserve
// ReservationBytes before persistence and settle NetDeltaBytes after success.
func EstimateIngestStorageUsage(
	doc *Document,
	vector []float32,
	previousSummary, nextSummary string,
) (IngestStorageUsage, error) {
	var usage IngestStorageUsage
	if doc != nil {
		usage.DocumentBytes = int64(
			len(doc.SessionID) +
				len(doc.Speaker) +
				len(doc.Original) +
				len(doc.Summary),
		)
		encoded, err := json.Marshal(vector)
		if err != nil {
			return IngestStorageUsage{}, fmt.Errorf("encode embedding for storage accounting: %w", err)
		}
		usage.EmbeddingBytes = int64(len(encoded))
	}
	usage.SummaryDeltaBytes = int64(len(nextSummary) - len(previousSummary))
	positiveSummaryDelta := usage.SummaryDeltaBytes
	if positiveSummaryDelta < 0 {
		positiveSummaryDelta = 0
	}
	usage.ReservationBytes = usage.DocumentBytes + usage.EmbeddingBytes + positiveSummaryDelta
	usage.NetDeltaBytes = usage.DocumentBytes + usage.EmbeddingBytes + usage.SummaryDeltaBytes
	return usage, nil
}
