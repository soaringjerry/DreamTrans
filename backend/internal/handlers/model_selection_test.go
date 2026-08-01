package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/modelcatalog"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/gorilla/websocket"
)

type purposeModelCatalogStub struct {
	mu     sync.Mutex
	models map[string]string
	errors map[string]error
	calls  []string
}

func (s *purposeModelCatalogStub) EffectiveModel(
	_ context.Context,
	_ string,
	purpose string,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, purpose)
	if err := s.errors[purpose]; err != nil {
		return "", err
	}
	return s.models[purpose], nil
}

func (s *purposeModelCatalogStub) IsAllowed(
	context.Context,
	string,
	string,
) (bool, error) {
	return true, nil
}

func (s *purposeModelCatalogStub) purposes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func requestWithModelClaims(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	return request.WithContext(context.WithValue(
		request.Context(),
		auth.UserClaimsKey,
		&auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
}

func TestWebSocketTranslationDoesNotRequireSummaryOrChat(t *testing.T) {
	catalog := &purposeModelCatalogStub{
		models: map[string]string{
			modelcatalog.PurposeTranslation: "translation-model",
		},
		errors: map[string]error{
			modelcatalog.PurposeSummary: errors.New("summary unavailable"),
			modelcatalog.PurposeChat:    errors.New("chat must not be resolved"),
		},
	}
	handler := &WebSocketHandler{
		modelCatalog: catalog,
		connections:  newWebSocketConnectionLimiter(4, 4),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.Handle(w, r.WithContext(context.WithValue(
			r.Context(),
			auth.UserClaimsKey,
			&auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
		)))
	}))
	t.Cleanup(server.Close)
	connection, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws/translate",
		nil,
	)
	if err != nil {
		t.Fatalf("translation WebSocket rejected because summary is unavailable: %v", err)
	}
	_ = connection.Close()
	want := []string{modelcatalog.PurposeTranslation, modelcatalog.PurposeSummary}
	if got := catalog.purposes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved purposes = %#v, want %#v", got, want)
	}
}

func TestRAGAskModelSelectionOnlyRequiresChat(t *testing.T) {
	catalog := &purposeModelCatalogStub{
		errors: map[string]error{
			modelcatalog.PurposeChat:        errors.New("chat unavailable"),
			modelcatalog.PurposeTranslation: errors.New("translation must not be resolved"),
			modelcatalog.PurposeSummary:     errors.New("summary must not be resolved"),
		},
	}
	handler := &RAGHandler{modelCatalog: catalog}
	response := httptest.NewRecorder()
	handler.HandleAsk(response, requestWithModelClaims(
		http.MethodPost,
		"/api/rag/ask",
		`{"session_id":"session-1","question":"hello"}`,
	))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	want := []string{modelcatalog.PurposeChat}
	if got := catalog.purposes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved purposes = %#v, want %#v", got, want)
	}
}

func TestArtifactModelSelectionOnlyRequiresSummary(t *testing.T) {
	catalog := &purposeModelCatalogStub{
		errors: map[string]error{
			modelcatalog.PurposeSummary:     errors.New("summary unavailable"),
			modelcatalog.PurposeTranslation: errors.New("translation must not be resolved"),
			modelcatalog.PurposeChat:        errors.New("chat must not be resolved"),
		},
	}
	handler := &RAGHandler{modelCatalog: catalog}
	response := httptest.NewRecorder()
	handler.HandleArtifacts(response, requestWithModelClaims(
		http.MethodPost,
		"/api/ai/artifacts",
		`{"session_id":"session-1","artifact_type":"summary"}`,
	))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	want := []string{modelcatalog.PurposeSummary}
	if got := catalog.purposes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved purposes = %#v, want %#v", got, want)
	}
}

func TestRAGServiceErrorStatusOnlyUses502ForProviderFailures(t *testing.T) {
	if got := ragServiceErrorStatus(errors.New("database unavailable")); got != http.StatusInternalServerError {
		t.Fatalf("local failure status = %d, want %d", got, http.StatusInternalServerError)
	}
	providerErr := fmt.Errorf("generate answer: %w", rag.ErrProviderRequest)
	if got := ragServiceErrorStatus(providerErr); got != http.StatusBadGateway {
		t.Fatalf("provider failure status = %d, want %d", got, http.StatusBadGateway)
	}
}
