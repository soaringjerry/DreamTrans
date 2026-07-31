package rag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIEmbedBatchUsesFixedDimensionsAndRestoresOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		inputs, ok := request.Input.([]any)
		if !ok || len(inputs) != 2 {
			t.Fatalf("unexpected inputs: %#v", request.Input)
		}
		if request.Dimensions != productionEmbeddingDimensions {
			t.Fatalf("dimensions=%d", request.Dimensions)
		}
		first := make([]float32, productionEmbeddingDimensions)
		second := make([]float32, productionEmbeddingDimensions)
		first[0] = 1
		second[0] = 2
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": second},
				{"index": 0, "embedding": first},
			},
			"usage": map[string]int{"prompt_tokens": 17, "total_tokens": 17},
		})
	}))
	defer server.Close()
	provider := &openAIEmbeddingProvider{
		baseURL: server.URL, apiKey: "test", model: "text-embedding-3-small",
		dimensions: productionEmbeddingDimensions, timeout: time.Second,
		httpClient: server.Client(),
	}
	vectors, tokens, err := provider.EmbedBatchWithUsage(
		context.Background(),
		[]string{"first", "second"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tokens != 17 || vectors[0][0] != 1 || vectors[1][0] != 2 {
		t.Fatalf("vectors/tokens = %#v/%d", vectors, tokens)
	}
}

func TestOpenAIEmbedBatchRejectsWrongDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"index": 0, "embedding": []float32{1, 2}}},
			"usage": map[string]int{"prompt_tokens": 1},
		})
	}))
	defer server.Close()
	provider := &openAIEmbeddingProvider{
		baseURL: server.URL, apiKey: "test", model: "custom",
		dimensions: productionEmbeddingDimensions, timeout: time.Second,
		httpClient: server.Client(),
	}
	if _, _, err := provider.EmbedBatchWithUsage(
		context.Background(),
		[]string{"first"},
	); !errors.Is(err, ErrInvalidEmbeddingDimension) {
		t.Fatalf("wrong embedding dimension error = %v, want ErrInvalidEmbeddingDimension", err)
	}
}
