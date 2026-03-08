package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jerkeyray/heimdall"
)

// OpenAI implements heimdall.
type OpenAI struct {
	APIKey string
}

// openAIRequest is the body sent to OpenAI. 
type openAIRequest struct {
	Model         string              `json:"model"` // model name
	Messages      []heimdall.Message  `json:"messages"` // Heimdall's message shape matches OpenAI
	Stream        bool                `json:"stream"` // tells OpenAI to SSE stream the response
	StreamOptions openAIStreamOptions `json:"stream_options"` // needed to receive token count in final chunk
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIResponse is the shape of each SSE data line from OpenAI. 
type openAIResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"` // token text for normal chunks
		} `json:"delta"`
	} `json:"choices"`
	// only appears in the final chunk before [DONE]
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// BuildRequest constructs an upstream HTTP request
// take Heimdall's ChatRequest -> convert to openAIRequest -> marshall to JSON -> create HTTP post
func (o *OpenAI) BuildRequest(ctx context.Context, req heimdall.ChatRequest) (*http.Request, error) {
	body := openAIRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true,
		StreamOptions: openAIStreamOptions{
			IncludeUsage: true,
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}

	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+o.APIKey)

	return r, nil
}

// ParseEvent is translaton layer of the adapter.
func (o *OpenAI) ParseEvent(_ string, data string) (*heimdall.Chunk, error) {
	// OpenAI's stream eliminator - must be caught before JSON unmarshal.
	if data == "[DONE]" {
		return &heimdall.Chunk{Done: true}, nil
	}

	// unmarshal rest into openAIResponse
	var resp openAIResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, fmt.Errorf("openai: parse event: %w", err)
	}

	chunk := &heimdall.Chunk{}

	if len(resp.Choices) > 0 {
		chunk.Content = resp.Choices[0].Delta.Content
	}

	// populate usage fields
	if resp.Usage != nil {
		chunk.Usage = &heimdall.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	return chunk, nil
}
