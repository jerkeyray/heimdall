# Heimdall — Product Requirements Document

---

## Overview

Heimdall is a minimal LLM streaming proxy written in Go. It accepts a unified chat request, translates it to the target provider's format, streams the response back to the client as normalized SSE chunks in real time. All provider-specific concerns are isolated behind a single interface — the rest of the codebase only speaks Heimdall's canonical types.

---

## Project Structure

```
heimdall/
├── heimdall.go              # canonical types, Provider interface
├── main.go                  # server bootstrap, routes, env config
├── handler.go               # request lifecycle, context, cancellation
├── stream/
│   └── streamer.go          # flush loop, TTFT, disconnect detection
└── provider/
    ├── openai.go            # OpenAI -> heimdall translation
    └── anthropic.go         # Anthropic -> heimdall translation
```

---

## Canonical Types — `heimdall.go`

This file is the contract everything else is built against. No provider-specific types leave their adapter file.

```go
type ChatRequest struct {
    Provider string    `json:"provider"`  // "openai" | "anthropic"
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
}

type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type Chunk struct {
    Content string
    Done    bool
    Err     error
    Usage   *Usage
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}

type Metrics struct {
    TTFT  time.Duration
    Total time.Duration
}

type Provider interface {
    BuildRequest(ctx context.Context, req ChatRequest) (*http.Request, error)
    ParseEvent(eventType, data string) (*Chunk, error)
}
```

### Design Decisions on Types

**`ParseEvent(eventType, data string)`** instead of `ParseLine(line string)` — Anthropic sends typed SSE events (`event: content_block_delta` + `data: {...}`), while OpenAI uses untyped `data:` lines with a `data: [DONE]` sentinel. The streamer handles raw SSE parsing (splitting `event:` and `data:` fields) and passes both to the provider. OpenAI adapters ignore the event type. This keeps SSE framing logic in one place and gives each provider the information it actually needs.

---

## API

### `POST /chat`

**Request**
```json
{
  "provider": "openai",
  "model": "gpt-4o",
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}
```

**Response — SSE stream**
```
data: {"content":"Hello","done":false}

data: {"content":" there","done":false}

data: {"content":"","done":true,"usage":{"input_tokens":10,"output_tokens":6},"metrics":{"ttft_ms":142,"total_ms":890}}
```

**Error mid-stream** — injected as a final chunk, then stream closes:
```
data: {"content":"","done":true,"error":"provider error: rate limit exceeded"}
```

**Pre-stream errors** return standard HTTP responses:
- `400` — malformed request, missing fields
- `400` — unknown provider
- `500` — upstream connection failure

Once the first SSE line is written, errors are delivered as a final chunk (shown above). This avoids mixing protocols mid-response.

### `GET /health`

Returns `200 OK`. No body.

---

## Design Decisions

### Streaming Only

No non-streaming mode. The entire purpose of this proxy is real-time token delivery. If a caller wants a complete response, they accumulate chunks client-side. This keeps the server code single-path and simple.

### No Proxy Authentication

Heimdall assumes it runs in a trusted network (behind a gateway, in a private subnet, etc.). Adding auth is a later concern — it would be middleware that doesn't touch the core streaming logic.

### Timeouts

- **Upstream connect timeout**: 10 seconds. If the provider doesn't accept the connection, fail fast with a pre-stream HTTP error.
- **Idle timeout**: 30 seconds. If no data arrives from the provider for 30s mid-stream, treat it as an error, send a final error chunk, and close.

### Graceful Shutdown

On `SIGINT`/`SIGTERM`:
1. Stop accepting new connections immediately.
2. Give in-flight streams up to 30 seconds to complete.
3. After the deadline, force-close remaining connections.

Uses `http.Server.Shutdown` with a context deadline.

### Logging

`log/slog` (stdlib, Go 1.21+). Structured JSON in production, text in development. No external logging dependencies. Every completed stream logs: provider, model, TTFT, total duration, token counts.

### Testing

`net/http/httptest` servers that replay recorded SSE responses. Each provider gets tests that verify the full path: raw SSE bytes in, `[]Chunk` out. The streamer gets tests with a mock provider to verify flush behavior, TTFT measurement, and disconnect handling. No real API calls in tests.

---

## Component Responsibilities

### `heimdall.go`
- Canonical types only
- `Provider` interface definition
- Zero logic, zero imports beyond `context`, `net/http`, `time`

### `main.go`
- Read `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `PORT` from environment
- Build provider registry (`map[string]Provider`)
- Register routes (`/chat`, `/health`)
- Start server with graceful shutdown

### `handler.go`
- Decode `ChatRequest` from request body
- Validate required fields (provider, model, messages non-empty)
- Look up provider from registry; 400 if unknown
- Set SSE headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
- Pass `r.Context()` through to streamer (carries client disconnect)
- All pre-stream errors are normal HTTP responses

### `stream/streamer.go`
- Assert `http.Flusher` support, fail fast if missing
- Call `provider.BuildRequest`, execute upstream HTTP request
- Parse raw SSE: buffer lines, extract `event:` type and `data:` payload
- Call `provider.ParseEvent(eventType, data)` for each complete event
- Write JSON-encoded chunk to client, flush after each
- Record TTFT on first chunk with non-empty content
- Watch `ctx.Done()` — on client disconnect, close upstream response body (cancels the upstream request)
- On stream end, populate final chunk with metrics and log

### `provider/openai.go`
- Implement `Provider` for OpenAI-compatible APIs
- `BuildRequest`: construct POST to `https://api.openai.com/v1/chat/completions` with `stream: true` and `stream_options: {"include_usage": true}`
- `ParseEvent`: ignore event type; parse JSON from data field; handle `[DONE]` sentinel; extract `choices[0].delta.content` and usage from final chunk

### `provider/anthropic.go`
- Implement `Provider` for Anthropic Messages API
- `BuildRequest`: construct POST to `https://api.anthropic.com/v1/messages` with `stream: true`
- `ParseEvent`: switch on event type — `content_block_delta` for tokens, `message_delta` for usage, `message_stop` for termination, `error` for mid-stream errors

---

## Translation Contract

| Concern | OpenAI | Anthropic | Heimdall |
|---|---|---|---|
| Termination | `data: [DONE]` | `event: message_stop` | `Chunk.Done = true` |
| Token content | `choices[0].delta.content` | `delta.text` | `Chunk.Content` |
| Usage | final chunk `usage` field | `message_delta` event | `Chunk.Usage` |
| Error | HTTP error / `error` in chunk | `error` event | `Chunk.Err` |

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `OPENAI_API_KEY` | For OpenAI | — | OpenAI API key |
| `ANTHROPIC_API_KEY` | For Anthropic | — | Anthropic API key |
| `PORT` | No | `8080` | Server listen port |

---

## Success Criteria

1. Tokens stream progressively — client sees tokens arrive in real time, not buffered
2. Client disconnect cancels the upstream request within 1 second
3. Both providers work through the same `/chat` endpoint with identical response format
4. No provider-specific types exist outside their adapter file
5. TTFT and total duration logged on every completed request
6. No goroutine leaks under disconnect, error, or repeated-request scenarios
7. All tests pass using mock servers with no external API calls
