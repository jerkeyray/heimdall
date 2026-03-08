package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jerkeyray/heimdall"
)

var testAnthropic = &Anthropic{APIKey: "test-key"}

// --- ParseEvent ---

func TestAnthropic_ParseEvent_ContentBlockDelta(t *testing.T) {
	data := `{"delta":{"type":"text_delta","text":"Hello"}}`
	chunk, err := testAnthropic.ParseEvent("content_block_delta", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Content != "Hello" {
		t.Errorf("expected content %q, got %q", "Hello", chunk.Content)
	}
	if chunk.Done {
		t.Error("expected Done=false")
	}
}

func TestAnthropic_ParseEvent_MessageStart(t *testing.T) {
	data := `{"message":{"usage":{"input_tokens":42}}}`
	chunk, err := testAnthropic.ParseEvent("message_start", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Usage == nil {
		t.Fatal("expected Usage to be populated")
	}
	if chunk.Usage.InputTokens != 42 {
		t.Errorf("expected InputTokens=42, got %d", chunk.Usage.InputTokens)
	}
	if chunk.Usage.OutputTokens != 0 {
		t.Errorf("expected OutputTokens=0, got %d", chunk.Usage.OutputTokens)
	}
}

func TestAnthropic_ParseEvent_MessageDelta(t *testing.T) {
	data := `{"usage":{"output_tokens":15}}`
	chunk, err := testAnthropic.ParseEvent("message_delta", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Usage == nil {
		t.Fatal("expected Usage to be populated")
	}
	if chunk.Usage.OutputTokens != 15 {
		t.Errorf("expected OutputTokens=15, got %d", chunk.Usage.OutputTokens)
	}
	if chunk.Usage.InputTokens != 0 {
		t.Errorf("expected InputTokens=0, got %d", chunk.Usage.InputTokens)
	}
}

func TestAnthropic_ParseEvent_MessageStop(t *testing.T) {
	chunk, err := testAnthropic.ParseEvent("message_stop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chunk.Done {
		t.Error("expected Done=true")
	}
}

func TestAnthropic_ParseEvent_Error(t *testing.T) {
	data := `{"error":{"message":"rate limit exceeded"}}`
	chunk, err := testAnthropic.ParseEvent("error", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Err == nil {
		t.Fatal("expected Chunk.Err to be set")
	}
	if chunk.Err.Error() != "anthropic: rate limit exceeded" {
		t.Errorf("unexpected error message: %s", chunk.Err.Error())
	}
}

func TestAnthropic_ParseEvent_SkippedEvents(t *testing.T) {
	// These events carry no useful data — should return nil, nil
	for _, eventType := range []string{"ping", "content_block_start", "content_block_stop"} {
		chunk, err := testAnthropic.ParseEvent(eventType, "{}")
		if err != nil {
			t.Errorf("%s: unexpected error: %v", eventType, err)
		}
		if chunk != nil {
			t.Errorf("%s: expected nil chunk, got %+v", eventType, chunk)
		}
	}
}

func TestAnthropic_ParseEvent_InvalidJSON(t *testing.T) {
	for _, eventType := range []string{"content_block_delta", "message_start", "message_delta", "error"} {
		_, err := testAnthropic.ParseEvent(eventType, "not json")
		if err == nil {
			t.Errorf("%s: expected error on invalid JSON, got nil", eventType)
		}
	}
}

// --- BuildRequest ---

func TestAnthropic_BuildRequest_Method_URL_Headers(t *testing.T) {
	req := heimdall.ChatRequest{
		Model:    "claude-opus-4-6",
		Messages: []heimdall.Message{{Role: "user", Content: "hi"}},
	}

	r, err := testAnthropic.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.Method != "POST" {
		t.Errorf("expected POST, got %s", r.Method)
	}
	if r.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("unexpected URL: %s", r.URL.String())
	}
	if r.Header.Get("x-api-key") != "test-key" {
		t.Errorf("unexpected x-api-key: %s", r.Header.Get("x-api-key"))
	}
	if r.Header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("unexpected anthropic-version: %s", r.Header.Get("anthropic-version"))
	}
	if r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
	}
}

func TestAnthropic_BuildRequest_Body(t *testing.T) {
	req := heimdall.ChatRequest{
		Model:    "claude-opus-4-6",
		Messages: []heimdall.Message{{Role: "user", Content: "hello"}},
	}

	r, err := testAnthropic.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body anthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if body.Model != "claude-opus-4-6" {
		t.Errorf("expected model claude-opus-4-6, got %s", body.Model)
	}
	if !body.Stream {
		t.Error("expected stream=true")
	}
	if body.MaxTokens != 4096 {
		t.Errorf("expected max_tokens=4096, got %d", body.MaxTokens)
	}
	if len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
		t.Error("messages not passed through correctly")
	}
}
