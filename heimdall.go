package heimdall

import (
	"context"
	"net/http"
	"time"
)

// ChatRequest is what the client will send to Heimdall. 
type ChatRequest struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message is a single turn in the conversation. Role can be either user, assistant or system.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chunk is the normalized unit of streaming output. Every token an LLM produces becomes a chunk.
type Chunk struct {
	Content string // token text
	Done    bool // signals the stream is over
	Err     error // any mid-stream errors 
	Usage   *Usage // only populated in the final chunk 
}

// Token consumption per request.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Metrics shows per request timing.
type Metrics struct {
	TTFT  time.Duration // Time To First Token
	Total time.Duration // Time from request to stream close
}

// Provider is the interface every adapter should satisfy.
type Provider interface {
	BuildRequest(ctx context.Context, req ChatRequest) (*http.Request, error)
	ParseEvent(eventType, data string) (*Chunk, error)
}
