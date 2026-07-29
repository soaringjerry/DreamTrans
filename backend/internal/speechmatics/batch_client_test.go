package speechmatics

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

type batchRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn batchRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSubmitJobReaderStreamsMultipart(t *testing.T) {
	const audio = "audio-bytes"
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.ContentLength > 0 {
			t.Fatalf("ContentLength = %d, want streaming request", request.ContentLength)
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(part.FileName(), "\r\n/\\") {
			t.Fatalf("unsafe multipart filename %q", part.FileName())
		}
		payload, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != audio {
			t.Fatalf("audio = %q, want %q", payload, audio)
		}
		for {
			next, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if _, nextErr = io.Copy(io.Discard, next); nextErr != nil {
				t.Fatal(nextErr)
			}
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"job-1","status":"running"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	response, err := client.SubmitJobReaderContext(
		context.Background(),
		strings.NewReader(audio),
		int64(len(audio)),
		"../unsafe\r\nname.wav",
		&JobConfig{Type: "transcription", TranscriptionConfig: TranscriptionConfig{Language: "en"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "job-1" {
		t.Fatalf("job ID = %q", response.ID)
	}
}

func TestSubmitJobReaderRejectsSizeMismatch(t *testing.T) {
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"job-1","status":"running"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := client.SubmitJobReaderContext(
		context.Background(),
		strings.NewReader("longer than declared"),
		4,
		"audio.wav",
		&JobConfig{Type: "transcription", TranscriptionConfig: TranscriptionConfig{Language: "en"}},
	)
	if err == nil {
		t.Fatal("size mismatch succeeded")
	}
}

func TestSubmitJobReaderRejectsInvalidProtocolConfig(t *testing.T) {
	client := NewBatchClient("test-key")
	for _, config := range []*JobConfig{
		nil,
		{Type: "other", TranscriptionConfig: TranscriptionConfig{Language: "en"}},
		{Type: "transcription", Reference: "bad\r\nreference", TranscriptionConfig: TranscriptionConfig{Language: "en"}},
		{Type: "transcription", TranscriptionConfig: TranscriptionConfig{Language: "en\r\nx"}},
		{Type: "transcription", TranscriptionConfig: TranscriptionConfig{
			Language: "en",
			MaxDelay: math.NaN(),
		}},
	} {
		_, err := client.SubmitJobReaderContext(
			context.Background(),
			strings.NewReader("audio"),
			5,
			"audio.wav",
			config,
		)
		if err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}

func TestSubmitJobNetworkFailureIsUncertain(t *testing.T) {
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	_, err := client.SubmitJobReaderContext(
		context.Background(),
		strings.NewReader("audio"),
		5,
		"audio.wav",
		&JobConfig{
			Type:                "transcription",
			Reference:           "batch-submit:request-1",
			TranscriptionConfig: TranscriptionConfig{Language: "en"},
		},
	)
	if !errors.Is(err, ErrBatchSubmissionUncertain) {
		t.Fatalf("submission error = %v, want ErrBatchSubmissionUncertain", err)
	}
}

func TestBatchAPIErrorDoesNotExposeResponseBody(t *testing.T) {
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("private transcript echoed upstream")),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := client.GetJobStatusContext(context.Background(), "job-1")
	if err == nil {
		t.Fatal("upstream error unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "private transcript") {
		t.Fatalf("upstream body leaked through error: %v", err)
	}
}

func TestDeleteJobContextForceCancelsAndBoundsResponse(t *testing.T) {
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", request.Method)
		}
		if request.URL.Path != "/v2/jobs/job-1" || request.URL.Query().Get("force") != "true" {
			t.Fatalf("unexpected delete URL: %s", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing authorization header")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"job":{"id":"job-1","status":"deleted"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	if err := client.DeleteJobContext(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForCompletionRejectsUnboundedDuration(t *testing.T) {
	client := NewBatchClient("test-key")
	for _, wait := range []time.Duration{0, -time.Second, maxBatchWait + time.Second} {
		if err := client.WaitForCompletionContext(context.Background(), "job-1", wait); err == nil {
			t.Fatalf("invalid wait duration %v accepted", wait)
		}
	}
}

func TestWaitForCompletionBoundsInFlightStatusRequest(t *testing.T) {
	client := NewBatchClient("test-key")
	client.httpClient = &http.Client{Transport: batchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}

	started := time.Now()
	err := client.WaitForCompletionContext(context.Background(), "job-1", 25*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("unexpected wait result: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("in-flight status request exceeded wait bound: %v", elapsed)
	}
}
