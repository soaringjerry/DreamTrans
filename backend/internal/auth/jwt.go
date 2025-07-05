package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type TokenGenerator struct {
	apiKey string
}

func NewTokenGenerator() (*TokenGenerator, error) {
	apiKey := os.Getenv("SM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SM_API_KEY environment variable not set")
	}
	return &TokenGenerator{apiKey: apiKey}, nil
}

// GenerateToken calls Speechmatics API to get a temporary key
func (tg *TokenGenerator) GenerateToken() (string, error) {
	// Create request body for RT temporary key with 10 minute TTL
	requestBody := map[string]interface{}{
		"ttl": 600, // 10 minutes in seconds
	}
	
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}
	
	// Create request to Speechmatics temporary key endpoint
	req, err := http.NewRequest("POST", "https://mp.speechmatics.com/v1/api_keys?type=rt", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tg.apiKey)
	
	// Make request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()
	
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	
	// Check for success status (200 OK or 201 Created)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("Speechmatics API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	// Parse response
	var response struct {
		KeyValue string `json:"key_value"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	
	if response.KeyValue == "" {
		return "", fmt.Errorf("no key_value in response")
	}
	
	return response.KeyValue, nil
}