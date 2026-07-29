package handlers

import (
	"encoding/json"
	"log"
	"net/http"
)

// encodeJSONResponse logs transport failures after response headers may already
// have been committed. At that point a second HTTP error response is unsafe.
func encodeJSONResponse(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("failed to encode HTTP response: %v", err)
	}
}

func writeHTTPResponse(w http.ResponseWriter, payload []byte) bool {
	// Callers set a non-HTML Content-Type and payloads are either JSON-encoded
	// or constant protocol responses.
	//nolint:gosec // G705: this helper does not render payloads as HTML.
	if _, err := w.Write(payload); err != nil {
		log.Printf("failed to write HTTP response: %v", err)
		return false
	}
	return true
}
