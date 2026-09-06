//go:build event_worker

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	busv1 "github.com/soaringjerry/pcas/gen/go/pcas/bus/v1"
	eventsv1 "github.com/soaringjerry/pcas/gen/go/pcas/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dreamtrans/backend/internal/speechmatics"
)

func main() {
	if err := runEventWorker(); err != nil {
		log.Fatal(err)
	}
}

func runEventWorker() error {
	pcasAddr := os.Getenv("PCAS_ADDR")
	if pcasAddr == "" {
		pcasAddr = "127.0.0.1:50051"
	}

	// Event-bus audio has no user who could have joined the training program,
	// so prefer the no-training account when one is configured.
	apiKey := strings.TrimSpace(os.Getenv("SM_API_KEY_NO_TRAINING"))
	if apiKey == "" {
		apiKey = os.Getenv("SM_API_KEY")
	}
	if apiKey == "" {
		log.Fatal("SM_API_KEY is required")
	}

	transportCredentials, secureTransport, err := pcasCredentials(pcasAddr)
	if err != nil {
		log.Fatalf("configure PCAS transport: %v", err)
	}
	conn, err := grpc.NewClient(
		pcasAddr,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(8<<20),
			grpc.MaxCallSendMsgSize(8<<20),
		),
	)
	if err != nil {
		log.Fatalf("connect PCAS failed: %v", err)
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 10*time.Second)
	conn.Connect()
	if err := waitForGRPCReady(dialCtx, conn); err != nil {
		cancelDial()
		_ = conn.Close()
		log.Fatalf("connect PCAS failed: %v", err)
	}
	cancelDial()
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("close PCAS connection: %v", err)
		}
	}()

	client := busv1.NewEventBusServiceClient(conn)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if key := strings.TrimSpace(os.Getenv("PCAS_API_KEY")); key != "" {
		if !secureTransport && !isLoopbackPCASAddress(pcasAddr) {
			return errors.New("refusing to send PCAS_API_KEY over a remote plaintext connection")
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key)
	}
	sub, err := client.Subscribe(ctx, &busv1.SubscribeRequest{ClientId: "dreamtrans-dapp"})
	if err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	batch := speechmatics.NewBatchClient(apiKey)
	audioHTTPClient := newRestrictedAudioHTTPClient()
	log.Printf("DreamTrans event-worker connected to PCAS at %s", strconv.Quote(pcasAddr))

	for {
		evt, err := sub.Recv()
		if err != nil {
			if ctx.Err() != nil {
				log.Println("DreamTrans event-worker stopped")
				return nil
			}
			return fmt.Errorf("subscribe receive: %w", err)
		}

		if evt.GetType() != "capability.audio.transcribe.request.v1" {
			continue
		}

		traceID := evt.GetTraceId()
		reqID := evt.GetId()
		userID := evt.GetUserId()
		sessionID := evt.GetSessionId()

		eventCtx, cancelEvent := context.WithTimeout(ctx, 12*time.Minute)
		audioBytes, filename, language, derr := extractAudioAndLang(eventCtx, audioHTTPClient, evt.GetData())
		if derr != nil {
			cancelEvent()
			log.Printf("event %s: bad payload: %s", strconv.Quote(reqID), strconv.Quote(derr.Error()))
			publishError(ctx, client, reqID, traceID, userID, sessionID, "invalid audio payload")
			continue
		}

		text, terr := transcribeOnce(eventCtx, batch, audioBytes, filename, language)
		cancelEvent()
		if terr != nil {
			log.Printf("event %s: transcribe error: %s", strconv.Quote(reqID), strconv.Quote(terr.Error()))
			publishError(ctx, client, reqID, traceID, userID, sessionID, "transcription failed")
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
			// reqID is escaped before logging; gosec's interprocedural taint
			// analysis does not currently recognize strconv.Quote here.
			//nolint:gosec // G706: quoted event identifier cannot inject log lines.
			log.Printf("event %s: response published (%d bytes)", strconv.Quote(reqID), len(text))
		}
	}
}

func waitForGRPCReady(ctx context.Context, conn *grpc.ClientConn) error {
	for {
		state := conn.GetState()
		switch state {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return errors.New("PCAS connection shut down before becoming ready")
		}
		if !conn.WaitForStateChange(ctx, state) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return errors.New("PCAS connection did not become ready")
		}
	}
}

func transcribeOnce(ctx context.Context, batch *speechmatics.BatchClient, audio []byte, filename, language string) (string, error) {
	jobCfg := &speechmatics.JobConfig{
		Type: "transcription",
		TranscriptionConfig: speechmatics.TranscriptionConfig{
			Language:       language,
			Diarization:    "speaker",
			EnablePartials: false,
			OperatingPoint: "enhanced",
		},
	}
	job, err := batch.SubmitJobContext(ctx, audio, filename, jobCfg)
	if err != nil {
		return "", err
	}
	if err := batch.WaitForCompletionContext(ctx, job.ID, 10*time.Minute); err != nil {
		return "", err
	}
	// Prefer txt for a single string text
	tr, err := batch.GetTranscriptContext(ctx, job.ID, "txt")
	if err == nil && tr != nil && tr.Content != "" {
		return tr.Content, nil
	}
	tr, err = batch.GetTranscriptContext(ctx, job.ID, "json-v2")
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
			if !strings.HasSuffix(r.Alternatives[0].Content, "\n") {
				b.WriteString(" ")
			}
		}
	}
	out := strings.TrimSpace(b.String())
	return out, nil
}

func pcasCredentials(address string) (credentials.TransportCredentials, bool, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, false, fmt.Errorf("PCAS_ADDR must include host and port: %w", err)
	}
	insecureConfigured, parseErr := strconv.ParseBool(strings.TrimSpace(os.Getenv("PCAS_INSECURE")))
	if parseErr != nil && strings.TrimSpace(os.Getenv("PCAS_INSECURE")) != "" {
		return nil, false, fmt.Errorf("PCAS_INSECURE must be true or false")
	}
	if insecureConfigured || (strings.TrimSpace(os.Getenv("PCAS_INSECURE")) == "" && isLoopbackPCASAddress(address)) {
		return insecure.NewCredentials(), false, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(os.Getenv("PCAS_TLS_SERVER_NAME")),
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = strings.Trim(host, "[]")
	}
	if caPath := strings.TrimSpace(os.Getenv("PCAS_CA_CERT")); caPath != "" {
		// The path is an operator-controlled startup setting, not request data.
		//nolint:gosec // G304: reading a configured local CA bundle is intentional.
		pem, readErr := os.ReadFile(caPath)
		if readErr != nil {
			return nil, false, fmt.Errorf("read PCAS_CA_CERT: %w", readErr)
		}
		roots, poolErr := x509.SystemCertPool()
		if poolErr != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, false, fmt.Errorf("PCAS_CA_CERT contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	return credentials.NewTLS(tlsConfig), true, nil
}

func isLoopbackPCASAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func publishError(ctx context.Context, client busv1.EventBusServiceClient, corrID, traceID, userID, sessionID, msg string) {
	m := map[string]interface{}{"code": "transcribe_error", "message": msg}
	val, _ := structpb.NewValue(m)
	payloadAny, _ := anypb.New(val)
	evt := &eventsv1.Event{
		Type:          "capability.audio.transcribe.error.v1",
		Source:        "dapp.dreamtrans",
		Specversion:   "1.0",
		Time:          timestamppb.Now(),
		TraceId:       traceID,
		CorrelationId: corrID,
		UserId:        userID,
		SessionId:     sessionID,
		Data:          payloadAny,
	}
	if _, err := client.Publish(ctx, evt); err != nil {
		log.Printf("publish error event failed: %v", err)
	}
}
