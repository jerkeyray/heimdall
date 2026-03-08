package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jerkeyray/heimdall"
)

// Anthropic implements heimdall.Provider for the Anthropic Messages API.
type Anthropic struct {
	APIKey string
}

// anthropicRequest is the body sent to Anthropic. Never leaves this file.
type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []heimdall.Message `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
}

// Unlike openAI, Anthropic has typed SSE events.

// anthropicContentBlockDelta carries the actual token text.
type anthropicContentBlockDelta struct {
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

// anthropicMessageStart carries the input token count, arrives in the beginning.
type anthropicMessageStart struct {
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// anthropicMessageDelta carries output token count, arrives in the end.
type anthropicMessageDelta struct {
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}


// anthropicError for mid-stream error messages.
type anthropicError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Anthropic) BuildRequest(ctx context.Context, req heimdall.ChatRequest) (*http.Request, error) {
	body := anthropicRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		MaxTokens: 4096, // MaxTokens is hardcoded unlike openAI
		Stream:    true,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}

	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", a.APIKey) 
	r.Header.Set("anthropic-version", "2023-06-01") // api is versioned via headers instead of URL path

	return r, nil
}

func (a *Anthropic) ParseEvent(eventType, data string) (*heimdall.Chunk, error) {
	// Unlike ignoring the eventType, anthropic uses a switch for each type of payload.
	switch eventType {
	case "content_block_delta":
		var payload anthropicContentBlockDelta
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("anthropic: parse content_block_delta: %w", err)
		}
		return &heimdall.Chunk{Content: payload.Delta.Text}, nil

	case "message_start":
		var payload anthropicMessageStart
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("anthropic: parse message_start: %w", err)
		}
		return &heimdall.Chunk{
			Usage: &heimdall.Usage{
				InputTokens: payload.Message.Usage.InputTokens,
			},
		}, nil

	case "message_delta":
		var payload anthropicMessageDelta
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("anthropic: parse message_delta: %w", err)
		}
		return &heimdall.Chunk{
			Usage: &heimdall.Usage{
				OutputTokens: payload.Usage.OutputTokens,
			},
		}, nil

	// anthropic's stream terminator, equivalent to openAI's [DONE].
	case "message_stop":
		return &heimdall.Chunk{Done: true}, nil

	case "error":
		var payload anthropicError
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("anthropic: parse error event: %w", err)
		}
		return &heimdall.Chunk{Err: fmt.Errorf("anthropic: %s", payload.Error.Message)}, nil

	default:
		// ping, content_block_start, content_block_stop — safe to skip
		return nil, nil
	}
}
