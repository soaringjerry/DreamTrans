package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/dreamtrans/backend/internal/speechmatics"
)

// BatchTranscribeRequest represents the request body for batch transcription
type BatchTranscribeRequest struct {
	Language       string  `json:"language"`
	Diarization    string  `json:"diarization"`
	OperatingPoint string  `json:"operating_point"`
	MaxDelay       float64 `json:"max_delay"`
}

// BatchTranscribeResponse represents the response for batch transcription
type BatchTranscribeResponse struct {
	JobID      string                           `json:"job_id"`
	Status     string                           `json:"status"`
	Transcript *speechmatics.TranscriptResponse `json:"transcript,omitempty"`
	Error      string                           `json:"error,omitempty"`
}

// BatchTranscribeHandler handles batch transcription requests
type BatchTranscribeHandler struct {
	batchClient *speechmatics.BatchClient
}

// NewBatchTranscribeHandler creates a new batch transcribe handler
func NewBatchTranscribeHandler() (*BatchTranscribeHandler, error) {
	apiKey := os.Getenv("SM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SM_API_KEY environment variable not set")
	}

	return &BatchTranscribeHandler{
		batchClient: speechmatics.NewBatchClient(apiKey),
	}, nil
}

// HandleSubmit handles the submission of audio for batch transcription
func (h *BatchTranscribeHandler) HandleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get the audio file
	file, handler, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "Failed to get audio file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read audio data
	audioData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read audio file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Parse config from form
	configStr := r.FormValue("config")
	var reqConfig BatchTranscribeRequest
	if configStr != "" {
		if err := json.Unmarshal([]byte(configStr), &reqConfig); err != nil {
			http.Error(w, "Invalid config format: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Set defaults
	if reqConfig.Language == "" {
		reqConfig.Language = "en"
	}
	if reqConfig.Diarization == "" {
		reqConfig.Diarization = "speaker"
	}
	if reqConfig.OperatingPoint == "" {
		reqConfig.OperatingPoint = "enhanced"
	}

	// Create job config
	jobConfig := speechmatics.JobConfig{
		Type: "transcription",
		TranscriptionConfig: speechmatics.TranscriptionConfig{
			Language:       reqConfig.Language,
			Diarization:    reqConfig.Diarization,
			EnablePartials: true,
			OperatingPoint: reqConfig.OperatingPoint,
			MaxDelay:       reqConfig.MaxDelay,
		},
	}

	// Submit job
	jobResp, err := h.batchClient.SubmitJob(audioData, handler.Filename, &jobConfig)
	if err != nil {
		http.Error(w, "Failed to submit job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return job info
	resp := BatchTranscribeResponse{
		JobID:  jobResp.ID,
		Status: jobResp.Status,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// HandleStatus handles checking the status of a batch transcription job
func (h *BatchTranscribeHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		http.Error(w, "Missing job_id parameter", http.StatusBadRequest)
		return
	}

	// Get job status
	status, err := h.batchClient.GetJobStatus(jobID)
	if err != nil {
		http.Error(w, "Failed to get job status: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := BatchTranscribeResponse{
		JobID:  status.ID,
		Status: status.Status,
	}

	// If job is done, get the transcript
	if status.Status == "done" {
		transcript, err := h.batchClient.GetTranscript(jobID, "json-v2")
		if err != nil {
			resp.Error = "Failed to get transcript: " + err.Error()
		} else {
			resp.Transcript = transcript
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// HandleTranscribeAndWait handles submission and waits for completion
func (h *BatchTranscribeHandler) HandleTranscribeAndWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get the audio file
	file, handler, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "Failed to get audio file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read audio data
	audioData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read audio file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Parse config from form
	configStr := r.FormValue("config")
	var reqConfig BatchTranscribeRequest
	if configStr != "" {
		if err := json.Unmarshal([]byte(configStr), &reqConfig); err != nil {
			http.Error(w, "Invalid config format: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Set defaults
	if reqConfig.Language == "" {
		reqConfig.Language = "en"
	}
	if reqConfig.Diarization == "" {
		reqConfig.Diarization = "speaker"
	}
	if reqConfig.OperatingPoint == "" {
		reqConfig.OperatingPoint = "enhanced"
	}

	// Create job config
	jobConfig := speechmatics.JobConfig{
		Type: "transcription",
		TranscriptionConfig: speechmatics.TranscriptionConfig{
			Language:       reqConfig.Language,
			Diarization:    reqConfig.Diarization,
			EnablePartials: true,
			OperatingPoint: reqConfig.OperatingPoint,
			MaxDelay:       reqConfig.MaxDelay,
		},
	}

	// Submit job
	jobResp, err := h.batchClient.SubmitJob(audioData, handler.Filename, &jobConfig)
	if err != nil {
		http.Error(w, "Failed to submit job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Wait for completion (max 10 minutes)
	if err := h.batchClient.WaitForCompletion(jobResp.ID, 10*time.Minute); err != nil {
		resp := BatchTranscribeResponse{
			JobID:  jobResp.ID,
			Status: "error",
			Error:  err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("Failed to encode response: %v", err)
		}
		return
	}

	// Get transcript
	transcript, err := h.batchClient.GetTranscript(jobResp.ID, "json-v2")
	if err != nil {
		resp := BatchTranscribeResponse{
			JobID:  jobResp.ID,
			Status: "done",
			Error:  "Failed to get transcript: " + err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("Failed to encode response: %v", err)
		}
		return
	}

	// Return success response
	resp := BatchTranscribeResponse{
		JobID:      jobResp.ID,
		Status:     "done",
		Transcript: transcript,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}
