package handlers

import (
    "net/http"
    "os"
    "strconv"
    "strings"

    "github.com/dreamtrans/backend/internal/dict"
)

type DictHandler struct {
    svc *dict.Service
}

func NewDictHandler() (*DictHandler, error) {
    path := os.Getenv("DICT_DB_PATH")
    if strings.TrimSpace(path) == "" {
        // default under data dir
        path = "/app/data/dict.db"
    }
    // Open only if file exists; otherwise keep nil to avoid startup hard fail
    if _, err := os.Stat(path); err == nil {
        svc, err := dict.Open(path)
        if err != nil { return nil, err }
        return &DictHandler{svc: svc}, nil
    }
    return &DictHandler{svc: nil}, nil
}

func (h *DictHandler) Close() { if h != nil && h.svc != nil { _ = h.svc.Close() } }

// GET /api/dict?word=...
func (h *DictHandler) HandleLookup(w http.ResponseWriter, r *http.Request) {
    if h.svc == nil { http.Error(w, "dictionary not loaded", http.StatusServiceUnavailable); return }
    q := r.URL.Query().Get("word")
    if strings.TrimSpace(q) == "" { http.Error(w, "missing word", http.StatusBadRequest); return }
    e, err := h.svc.Lookup(r.Context(), q)
    if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
    if e == nil { writeJSON(w, map[string]any{"found": false}) ; return }
    writeJSON(w, map[string]any{"found": true, "entry": e})
}

// GET /api/dict/prefix?q=...&limit=10
func (h *DictHandler) HandlePrefix(w http.ResponseWriter, r *http.Request) {
    if h.svc == nil { http.Error(w, "dictionary not loaded", http.StatusServiceUnavailable); return }
    q := r.URL.Query().Get("q")
    if strings.TrimSpace(q) == "" { http.Error(w, "missing q", http.StatusBadRequest); return }
    limit := 10
    if v := r.URL.Query().Get("limit"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 { limit = n }
    }
    list, err := h.svc.LookupPrefix(r.Context(), q, limit)
    if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
    writeJSON(w, map[string]any{"items": list})
}

