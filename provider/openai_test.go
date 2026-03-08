package provider

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/jerkeyray/heimdall"
)

var testOpenAI = &OpenAI{APIKey: "test-key"}

// --- ParseEvent ---

func TestParseEvent_Token(t *testing.T) {
	data := `{"choices":[{"delta":{"content":"Hello"}}]}`
	chunk, err := testOpenAI.ParseEvent("", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Content != "Hello" {
		t.Errorf("expected content %q, got %q", "Hello", chunk.Content)
	}
	if chunk.Done {
		t.Error("expected Done=false")
	}
	if chunk.Usage != nil {
		t.Error("expected Usage=nil on token chunk")
	}
}

func TestParseEvent_EmptyDelta(t *testing.T) {
	// OpenAI sends empty deltas on the first and last chunks
	data := `{"choices":[{"delta":{}}]}`
	chunk, err := testOpenAI.ParseEvent("", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Content != "" {
		t.Errorf("expected empty content, got %q", chunk.Content)
	}
	if chunk.Done {
		t.Error("expected Done=false")
	}
}

func TestParseEvent_UsageChunk(t *testing.T) {
	// Final chunk before [DONE] carries usage and an empty delta
	data := `{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":6}}`
	chunk, err := testOpenAI.ParseEvent("", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunk.Usage == nil {
		t.Fatal("expected Usage to be populated")
	}
	if chunk.Usage.InputTokens != 10 {
		t.Errorf("expected InputTokens=10, got %d", chunk.Usage.InputTokens)
	}
	if chunk.Usage.OutputTokens != 6 {
		t.Errorf("expected OutputTokens=6, got %d", chunk.Usage.OutputTokens)
	}
}

func TestParseEvent_Done(t *testing.T) {
	chunk, err := testOpenAI.ParseEvent("", "[DONE]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chunk.Done {
		t.Error("expected Done=true")
	}
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	_, err := testOpenAI.ParseEvent("", "not json")
	if err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
}

func TestParseEvent_EventTypeIgnored(t *testing.T) {
	// OpenAI doesn't use event types — should behave identically regardless
	data := `{"choices":[{"delta":{"content":"hi"}}]}`
	c1, _ := testOpenAI.ParseEvent("", data)
	c2, _ := testOpenAI.ParseEvent("some_event_type", data)
	if c1.Content != c2.Content {
		t.Error("event type should be ignored")
	}
}

// --- BuildRequest ---

func TestBuildRequest_Method_URL_Headers(t *testing.T) {
	req := heimdall.ChatRequest{
		Provider: "openai",
		Model:    "gpt-4o",
		Messages: []heimdall.Message{{Role: "user", Content: "hi"}},
	}

	r, err := testOpenAI.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.Method != "POST" {
		t.Errorf("expected POST, got %s", r.Method)
	}
	if r.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("unexpected URL: %s", r.URL.String())
	}
	if r.Header.Get("Authorization") != "Bearer test-key" {
		t.Errorf("unexpected Authorization header: %s", r.Header.Get("Authorization"))
	}
	if r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
	}
}

func TestBuildRequest_Body(t *testing.T) {
	req := heimdall.ChatRequest{
		Model:    "gpt-4o",
		Messages: []heimdall.Message{{Role: "user", Content: "hello"}},
	}

	r, err := testOpenAI.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body openAIRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if body.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", body.Model)
	}
	if !body.Stream {
		t.Error("expected stream=true")
	}
	if !body.StreamOptions.IncludeUsage {
		t.Error("expected include_usage=true")
	}
	if len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
		t.Error("messages not passed through correctly")
	}
}

func TestBuildRequest_ContextPropagated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := heimdall.ChatRequest{
		Model:    "gpt-4o",
		Messages: []heimdall.Message{{Role: "user", Content: "hi"}},
	}

	r, err := testOpenAI.BuildRequest(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error building request: %v", err)
	}

	// The request should carry the cancelled context
	_, err = io.ReadAll(r.Body)
	if r.Context().Err() == nil {
		t.Error("expected request context to be cancelled")
	}
}
