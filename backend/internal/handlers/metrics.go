package handlers

import (
    "net/http"
    "github.com/dreamtrans/backend/internal/metrics"
)

func HandleMetrics(w http.ResponseWriter, r *http.Request) {
    snap := metrics.SnapshotMetrics()
    writeJSON(w, snap)
}

