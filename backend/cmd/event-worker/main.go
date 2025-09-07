//go:build event_worker

package main

import (
    "context"
    "encoding/base64"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "strings"
    "time"

    busv1 "github.com/soaringjerry/pcas/gen/go/pcas/bus/v1"
    eventsv1 "github.com/soaringjerry/pcas/gen/go/pcas/events/v1"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/protobuf/types/known/anypb"
    "google.golang.org/protobuf/types/known/structpb"
    "google.golang.org/protobuf/types/known/timestamppb"

    "github.com/dreamtrans/backend/internal/speechmatics"
)

func main() {
    pcasAddr := os.Getenv("PCAS_ADDR")
    if pcasAddr == "" {
        pcasAddr = "127.0.0.1:50051"
    }

    apiKey := os.Getenv("SM_API_KEY")
    if apiKey == "" {
        log.Fatal("SM_API_KEY is required")
    }

    conn, err := grpc.Dial(pcasAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("connect PCAS failed: %v", err)
    }
    defer conn.Close()

    client := busv1.NewEventBusServiceClient(conn)

    ctx := context.Background()
    sub, err := client.Subscribe(ctx, &busv1.SubscribeRequest{ClientId: "dreamtrans-dapp"})
    if err != nil {
        log.Fatalf("subscribe failed: %v", err)
    }

    batch := speechmatics.NewBatchClient(apiKey)
    log.Printf("DreamTrans event-worker connected to PCAS at %s", pcasAddr)

    for {
        evt, err := sub.Recv()
        if err != nil {
            log.Fatalf("subscribe recv error: %v", err)
        }

        if evt.GetType() != "capability.audio.transcribe.request.v1" {
            continue
        }

        traceID := evt.GetTraceId()
        reqID := evt.GetId()
        userID := evt.GetUserId()
        sessionID := evt.GetSessionId()

        audioBytes, filename, language, derr := extractAudioAndLang(evt.GetData())
        if derr != nil {
            log.Printf("event %s: bad payload: %v", reqID, derr)
            publishError(ctx, client, reqID, traceID, userID, sessionID, fmt.Sprintf("bad payload: %v", derr))
            continue
        }

        text, terr := transcribeOnce(batch, audioBytes, filename, language)
        if terr != nil {
            log.Printf("event %s: transcribe error: %v", reqID, terr)
            publishError(ctx, client, reqID, traceID, userID, sessionID, terr.Error())
            continue
        }

        respMap := map[string]interface{}{"text": text, "language": language}
        respVal, _ := structpb.NewValue(respMap)
        respAny, _ := anypb.New(respVal)

        resp := &eventsv1.Event{
            Type:          "capability.audio.transcribe.response.v1",
            Source:        "dapp.dreamtrans",
            Specversion:   "1.0",
            Time:          timestamppb.Now(),
            TraceId:       traceID,
            CorrelationId: reqID,
            UserId:        userID,
            SessionId:     sessionID,
            Data:          respAny,
        }
        if _, err := client.Publish(ctx, resp); err != nil {
            log.Printf("publish response failed: %v", err)
        } else {
            log.Printf("event %s: response published (%d bytes)", reqID, len(text))
        }
    }
}

func extractAudioAndLang(data *anypb.Any) ([]byte, string, string, error) {
    language := "en"
    if data == nil {
        return nil, "", language, fmt.Errorf("missing data")
    }
    val := &structpb.Value{}
    if err := data.UnmarshalTo(val); err != nil {
        return nil, "", language, fmt.Errorf("invalid data: %w", err)
    }
    m, ok := val.AsInterface().(map[string]interface{})
    if !ok {
        return nil, "", language, fmt.Errorf("data not object")
    }
    if v, ok := m["language"].(string); ok && v != "" {
        language = strings.ToLower(v)
    }
    if s, ok := m["audio_base64"].(string); ok && s != "" {
        b, err := base64.StdEncoding.DecodeString(s)
        if err != nil {
            return nil, "", language, fmt.Errorf("bad audio_base64: %w", err)
        }
        return b, "request.wav", language, nil
    }
    if u, ok := m["audio_url"].(string); ok && u != "" {
        resp, err := http.Get(u)
        if err != nil {
            return nil, "", language, fmt.Errorf("fetch audio_url failed: %w", err)
        }
        defer resp.Body.Close()
        if resp.StatusCode < 200 || resp.StatusCode >= 300 {
            body, _ := io.ReadAll(resp.Body)
            return nil, "", language, fmt.Errorf("fetch audio_url status %d: %s", resp.StatusCode, string(body))
        }
        b, err := io.ReadAll(resp.Body)
        if err != nil {
            return nil, "", language, fmt.Errorf("read audio_url failed: %w", err)
        }
        // try filename from URL path
        name := "request.bin"
        if idx := strings.LastIndex(u, "/"); idx >= 0 && idx+1 < len(u) {
            name = u[idx+1:]
        }
        return b, name, language, nil
    }
    return nil, "", language, fmt.Errorf("no audio_base64 or audio_url")
}

func transcribeOnce(batch *speechmatics.BatchClient, audio []byte, filename, language string) (string, error) {
    jobCfg := &speechmatics.JobConfig{
        Type: "transcription",
        TranscriptionConfig: speechmatics.TranscriptionConfig{
            Language:       language,
            Diarization:    "speaker",
            EnablePartials: false,
            OperatingPoint: "enhanced",
        },
    }
    job, err := batch.SubmitJob(audio, filename, jobCfg)
    if err != nil {
        return "", err
    }
    if err := batch.WaitForCompletion(job.ID, 10*time.Minute); err != nil {
        return "", err
    }
    // Prefer txt for a single string text
    tr, err := batch.GetTranscript(job.ID, "txt")
    if err == nil && tr != nil && tr.Content != "" {
        return tr.Content, nil
    }
    tr, err = batch.GetTranscript(job.ID, "json-v2")
    if err != nil {
        return "", err
    }
    if tr == nil {
        return "", fmt.Errorf("empty transcript")
    }
    var b strings.Builder
    for _, r := range tr.Results {
        if len(r.Alternatives) > 0 {
            b.WriteString(r.Alternatives[0].Content)
            if !strings.HasSuffix(b.String(), "\n") {
                b.WriteString(" ")
            }
        }
    }
    out := strings.TrimSpace(b.String())
    return out, nil
}

func publishError(ctx context.Context, client busv1.EventBusServiceClient, corrID, traceID, userID, sessionID, msg string) {
    m := map[string]interface{}{"code": "transcribe_error", "message": msg}
    val, _ := structpb.NewValue(m)
    any, _ := anypb.New(val)
    evt := &eventsv1.Event{
        Type:          "capability.audio.transcribe.error.v1",
        Source:        "dapp.dreamtrans",
        Specversion:   "1.0",
        Time:          timestamppb.Now(),
        TraceId:       traceID,
        CorrelationId: corrID,
        UserId:        userID,
        SessionId:     sessionID,
        Data:          any,
    }
    if _, err := client.Publish(ctx, evt); err != nil {
        log.Printf("publish error event failed: %v", err)
    }
}
