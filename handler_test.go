package heimdall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// noopProvider satisfies heimdall.Provider for tests that only need a registry
// entry — the real streaming is provided by the injected streamFn.
type noopProvider struct{}

func (n *noopProvider) BuildRequest(ctx context.Context, req ChatRequest) (*http.Request, error) {
	return nil, nil
}
func (n *noopProvider) ParseEvent(eventType, data string) (*Chunk, error) { return nil, nil }

// providers is the shared registry used in all handler tests.
var testProviders = map[string]Provider{
	"mock": &noopProvider{},
}

// post sends a POST /chat request through the handler and returns the recorder.
func post(body string, streamFn StreamFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ChatHandler(testProviders, streamFn)(rec, req)
	return rec
}

// errorBody decodes the JSON error field from the response body.
func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("failed to decode response body as JSON: %v (body: %s)", err, rec.Body.String())
	}
	return m["error"]
}

// --- Validation tests ---

func TestChatHandler_MalformedJSON(t *testing.T) {
	rec := post("not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if msg := errorBody(t, rec); msg != "invalid request body" {
		t.Errorf("unexpected error message: %q", msg)
	}
}

func TestChatHandler_MissingProvider(t *testing.T) {
	rec := post(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if msg := errorBody(t, rec); msg != "provider is required" {
		t.Errorf("unexpected error message: %q", msg)
	}
}

func TestChatHandler_MissingModel(t *testing.T) {
	rec := post(`{"provider":"mock","messages":[{"role":"user","content":"hi"}]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if msg := errorBody(t, rec); msg != "model is required" {
		t.Errorf("unexpected error message: %q", msg)
	}
}

func TestChatHandler_EmptyMessages(t *testing.T) {
	rec := post(`{"provider":"mock","model":"gpt-4o","messages":[]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if msg := errorBody(t, rec); msg != "messages is required" {
		t.Errorf("unexpected error message: %q", msg)
	}
}

func TestChatHandler_UnknownProvider(t *testing.T) {
	rec := post(`{"provider":"unknown","model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	msg := errorBody(t, rec)
	if !strings.Contains(msg, "unknown provider: unknown") {
		t.Errorf("unexpected error message: %q", msg)
	}
}

// --- Stream tests ---

func TestChatHandler_StreamSuccess(t *testing.T) {
	streamFn := func(ctx context.Context, w http.ResponseWriter, p Provider, req ChatRequest) error {
		fmt.Fprint(w, "data: {\"content\":\"hello\",\"done\":false}\n\n")
		fmt.Fprint(w, "data: {\"content\":\"\",\"done\":true}\n\n")
		return nil
	}

	rec := post(`{"provider":"mock","model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, streamFn)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if !strings.Contains(rec.Body.String(), `"hello"`) {
		t.Errorf("expected body to contain hello, got: %s", rec.Body.String())
	}
}

func TestChatHandler_StreamForwardsRequest(t *testing.T) {
	// Verify that the ChatRequest fields are forwarded correctly to streamFn.
	var received ChatRequest
	streamFn := func(ctx context.Context, w http.ResponseWriter, p Provider, req ChatRequest) error {
		received = req
		return nil
	}

	post(`{"provider":"mock","model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, streamFn)

	if received.Provider != "mock" {
		t.Errorf("provider: got %q, want %q", received.Provider, "mock")
	}
	if received.Model != "gpt-4o" {
		t.Errorf("model: got %q, want %q", received.Model, "gpt-4o")
	}
	if len(received.Messages) != 1 || received.Messages[0].Content != "hello" {
		t.Errorf("messages not forwarded correctly: %+v", received.Messages)
	}
}

func TestChatHandler_StreamError(t *testing.T) {
	streamFn := func(ctx context.Context, w http.ResponseWriter, p Provider, req ChatRequest) error {
		return errors.New("upstream connection refused")
	}

	rec := post(`{"provider":"mock","model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, streamFn)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
	msg := errorBody(t, rec)
	if !strings.Contains(msg, "upstream connection refused") {
		t.Errorf("unexpected error message: %q", msg)
	}
}
