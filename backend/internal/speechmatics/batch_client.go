package speechmatics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

const (
	batchAPIBaseURL = "https://asr.api.speechmatics.com/v2"
	defaultTimeout  = 30 * time.Second
)

// BatchClient handles interactions with Speechmatics Batch API
type BatchClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewBatchClient creates a new Speechmatics Batch API client
func NewBatchClient(apiKey string) *BatchClient {
	return &BatchClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// TranscriptionConfig represents the transcription configuration
type TranscriptionConfig struct {
	Language               string   `json:"language"`
	Diarization           string   `json:"diarization,omitempty"`
	EnablePartials        bool     `json:"enable_partials,omitempty"`
	OperatingPoint        string   `json:"operating_point,omitempty"`
	MaxDelay              float64  `json:"max_delay,omitempty"`
}

// JobConfig represents the job configuration
type JobConfig struct {
	Type                  string               `json:"type"`
	TranscriptionConfig   TranscriptionConfig  `json:"transcription_config"`
}

// JobResponse represents the response from job submission
type JobResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// TranscriptResponse represents the transcript retrieval response
type TranscriptResponse struct {
	Format   string `json:"format"`
	Content  string `json:"content"`
	Metadata struct {
		CreatedAt   string  `json:"created_at"`
		Duration    float64 `json:"duration"`
		Language    string  `json:"language"`
	} `json:"metadata"`
	Results []TranscriptResult `json:"results"`
}

// TranscriptResult represents a single transcript segment
type TranscriptResult struct {
	Alternatives []struct {
		Content    string  `json:"content"`
		Confidence float64 `json:"confidence"`
		Speaker    string  `json:"speaker,omitempty"`
	} `json:"alternatives"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Type      string  `json:"type"`
}

// SubmitJob submits an audio file for transcription
func (c *BatchClient) SubmitJob(audioData []byte, filename string, config JobConfig) (*JobResponse, error) {
	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add audio file
	part, err := writer.CreateFormFile("data_file", filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return nil, fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add config
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := writer.WriteField("config", string(configJSON)); err != nil {
		return nil, fmt.Errorf("failed to write config field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/jobs/", batchAPIBaseURL), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var jobResp JobResponse
	if err := json.Unmarshal(respBody, &jobResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &jobResp, nil
}

// GetJobStatus retrieves the status of a transcription job
func (c *BatchClient) GetJobStatus(jobID string) (*JobResponse, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/jobs/%s", batchAPIBaseURL, jobID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var jobResp JobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &jobResp, nil
}

// GetTranscript retrieves the transcript for a completed job
func (c *BatchClient) GetTranscript(jobID string, format string) (*TranscriptResponse, error) {
	if format == "" {
		format = "json-v2"
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/jobs/%s/transcript?format=%s", batchAPIBaseURL, jobID, format), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// If format is txt, return raw text
	if format == "txt" {
		return &TranscriptResponse{
			Format:  format,
			Content: string(respBody),
		}, nil
	}

	// Otherwise parse JSON
	var transcriptResp TranscriptResponse
	if err := json.Unmarshal(respBody, &transcriptResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &transcriptResp, nil
}

// WaitForCompletion polls the job status until it's completed or failed
func (c *BatchClient) WaitForCompletion(jobID string, maxWaitTime time.Duration) error {
	startTime := time.Now()
	pollInterval := 2 * time.Second

	for {
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("timeout waiting for job completion")
		}

		status, err := c.GetJobStatus(jobID)
		if err != nil {
			return fmt.Errorf("failed to get job status: %w", err)
		}

		switch status.Status {
		case "done":
			return nil
		case "rejected", "deleted", "error":
			return fmt.Errorf("job failed with status: %s", status.Status)
		}

		time.Sleep(pollInterval)
	}
}