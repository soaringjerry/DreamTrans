package handlers

import (
    "net/http"
    "github.com/dreamtrans/backend/internal/config"
)

// default prompts should mirror backend provider logic to keep consistent resets
// HandlePromptDefaults returns backend default prompts for chat/translation/summary.
func HandlePromptDefaults(w http.ResponseWriter, r *http.Request) {
    cfg := config.Get()
    writeJSON(w, map[string]any{
        "prompt_chat_default":      cfg.Prompts.Chat,
        "prompt_translate_default": cfg.Prompts.Translate,
        "prompt_summary_default":   cfg.Prompts.Summary,
    })
}
