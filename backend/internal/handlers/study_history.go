package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

type studyHistoryItem struct {
	store.StudyHistoryEntry
	Reveal *studyReveal `json:"reveal,omitempty"`
}

func (h *RAGHandler) handleStudyHistory(w http.ResponseWriter, r *http.Request, project *models.AIProject) {
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	if before != "" && uuid.Validate(before) != nil {
		http.Error(w, "invalid history cursor", http.StatusBadRequest)
		return
	}
	entries, err := h.store.ListStudyHistory(r.Context(), project.UserID, project.ID, before, 51)
	if err != nil {
		http.Error(w, "failed to load study history", http.StatusInternalServerError)
		return
	}
	nextCursor := ""
	if len(entries) > 50 {
		nextCursor = entries[49].ID
		entries = entries[:50]
	}
	items := make([]studyHistoryItem, 0, len(entries))
	for i := range entries {
		entry := &entries[i]
		item := studyHistoryItem{StudyHistoryEntry: *entry}
		var content studyScenarioContent
		if json.Unmarshal(entry.Scenario, &content) == nil && content.Question != "" {
			reveal := studyRevealFor(&content)
			item.Reveal = &reveal
		}
		items = append(items, item)
	}
	WriteJSON(w, map[string]any{"items": items, "next_cursor": nextCursor})
}
