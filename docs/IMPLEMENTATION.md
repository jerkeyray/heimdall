# Heimdall — Implementation Plan

---

## Phased Build Order

Each phase produces working, testable code. No phase depends on a later one.

---

### Phase 1: Canonical Types and Project Skeleton

**Files:** `heimdall.go`, `go.mod`

**Work:**
- `go mod init` with appropriate module path
- Define all canonical types in `heimdall.go`: `ChatRequest`, `Message`, `Chunk`, `Usage`, `Metrics`, `Provider` interface
- No logic — just types and the interface

**Done when:** Project compiles with `go build ./...`

---

### Phase 2: OpenAI Provider

**Files:** `provider/openai.go`, `provider/openai_test.go`

**Work:**
- Implement `OpenAI` struct holding the API key
- `BuildRequest`: marshal OpenAI-format body from `ChatRequest`, set headers (`Authorization: Bearer ...`, `Content-Type: application/json`), return `*http.Request` targeting `https://api.openai.com/v1/chat/completions` with `stream: true` and `stream_options: {"include_usage": true}`
- `ParseEvent`: ignore `eventType` param. If data is `[DONE]`, return `Chunk{Done: true}`. Otherwise unmarshal the JSON, extract `choices[0].delta.content` for `Chunk.Content`. On the final chunk before `[DONE]`, extract `usage` into `Chunk.Usage`.
- Internal types for OpenAI response parsing (unexported, never leave this file)

**Tests:**
- `ParseEvent` with recorded SSE data lines: normal tokens, empty deltas, usage chunk, `[DONE]`
- `BuildRequest` verifies URL, headers, body structure

---

### Phase 3: Anthropic Provider

**Files:** `provider/anthropic.go`, `provider/anthropic_test.go`

**Work:**
- Implement `Anthropic` struct holding the API key
- `BuildRequest`: marshal Anthropic-format body, set headers (`x-api-key`, `anthropic-version: 2023-06-01`, `Content-Type: application/json`), return `*http.Request` targeting `https://api.anthropic.com/v1/messages` with `stream: true`
- `ParseEvent`: switch on `eventType`:
  - `content_block_delta`: extract `delta.text` -> `Chunk.Content`
  - `message_delta`: extract `usage.output_tokens` -> `Chunk.Usage`
  - `message_stop`: return `Chunk{Done: true}`
  - `error`: parse error message -> `Chunk.Err`
  - All other event types (e.g., `message_start`, `content_block_start`, `content_block_stop`, `ping`): return `nil, nil` (skip)

**Tests:**
- `ParseEvent` with each event type using recorded data
- `BuildRequest` verifies URL, headers, body structure

---

### Phase 4: SSE Streamer

**Files:** `stream/streamer.go`, `stream/streamer_test.go`

**Work:**
- `Stream(ctx context.Context, w http.ResponseWriter, provider Provider, req ChatRequest) error`
- Assert `w.(http.Flusher)` — return error if not supported
- Call `provider.BuildRequest(ctx, req)` to get upstream `*http.Request`
- Execute with `http.Client` (10s connect timeout via `Transport.DialContext`)
- Check upstream response status — if not 2xx, read body and return error (caller handles pre-stream HTTP error)
- SSE parsing loop:
  - Read lines from response body with `bufio.Scanner`
  - Track current `event:` type (default empty string, reset after each blank line)
  - On `event: <type>` line, store the type
  - On `data: <payload>` line, call `provider.ParseEvent(eventType, payload)`
  - On blank line, reset event type
- For each non-nil `Chunk` returned:
  - If `Chunk.Err != nil`: write error as final JSON chunk, flush, return
  - Write JSON to `w` as `data: {...}\n\n`, flush
  - On first non-empty `Content`, record TTFT
  - If `Chunk.Done`: attach metrics to the JSON output, log, return
- Idle timeout: wrap response body reads with a deadline. If no data for 30s, send error chunk and close
- Context cancellation: `select` on `ctx.Done()` or use `ctx` in the HTTP request so closing the context closes the upstream body

**JSON output format per chunk:**
```go
type wireChunk struct {
    Content string     `json:"content"`
    Done    bool       `json:"done"`
    Error   string     `json:"error,omitempty"`
    Usage   *wireUsage `json:"usage,omitempty"`
    Metrics *wireMetrics `json:"metrics,omitempty"`
}
```

**Tests (using httptest):**
- Happy path: mock server sends 5 SSE events + done -> verify 5 content chunks + final chunk with usage/metrics
- Client disconnect mid-stream -> verify upstream request is cancelled
- Provider error mid-stream -> verify error chunk is written
- Idle timeout -> verify error chunk after no data
- Non-flusher ResponseWriter -> verify immediate error

---

### Phase 5: Handler

**Files:** `handler.go`, `handler_test.go`

**Work:**
- `ChatHandler(providers map[string]Provider) http.HandlerFunc`
- Decode JSON body into `ChatRequest`
  - Malformed JSON: 400 with `{"error": "invalid request body"}`
  - Missing/empty `provider`: 400 with `{"error": "provider is required"}`
  - Missing/empty `model`: 400 with `{"error": "model is required"}`
  - Empty `messages`: 400 with `{"error": "messages is required"}`
- Look up provider in map: unknown -> 400 `{"error": "unknown provider: X"}`
- Set SSE headers
- Call `stream.Stream(r.Context(), w, provider, req)`
- If `Stream` returns error (pre-stream failure like upstream connect timeout): 502 with error message

**Tests:**
- Validation errors (missing fields, unknown provider)
- Successful stream-through with mock provider

---

### Phase 6: Server Bootstrap

**Files:** `main.go`

**Work:**
- Read env vars: `PORT` (default `8080`), `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`
- Build provider map — only register providers whose API key is present. Log which providers are active.
- If no providers are configured, log fatal.
- Routes:
  - `POST /chat` -> `ChatHandler(providers)`
  - `GET /health` -> 200 OK
- Create `http.Server` with read/write timeouts
- Start in goroutine, block on signal (`SIGINT`, `SIGTERM`)
- On signal: `server.Shutdown(ctx)` with 30s deadline
- Log startup and shutdown events

---

## Implementation Notes

### SSE Parsing Detail

The streamer's SSE parser is simple and doesn't need to be spec-complete. The rules:

1. Lines starting with `event:` set the current event type (trimmed)
2. Lines starting with `data:` are payloads (strip prefix, trim leading space)
3. Blank lines terminate an event — call `ParseEvent`, reset event type
4. Lines starting with `:` are comments — skip
5. Multiple `data:` lines before a blank line get concatenated with `\n` (rare but spec-correct)

### Idle Timeout Implementation

Wrap the upstream response body in a reader that resets a `time.Timer` on each `Read()` call. If the timer fires, close the body. The `Scanner` will see an error on next read, and the stream loop handles it as a provider error.

### Context Propagation

`r.Context()` from the incoming HTTP request is cancelled when the client disconnects. This context is passed to `provider.BuildRequest` and used in the upstream `http.Client.Do` call. When the client disconnects:
1. `ctx.Done()` fires
2. The upstream HTTP request is cancelled (built-in `net/http` behavior)
3. The response body read returns an error
4. The stream loop exits, the handler returns, resources are freed

No extra goroutines needed for disconnect detection.

### No External Dependencies

The entire project uses only the Go standard library. No routers (stdlib `http.ServeMux` is sufficient in Go 1.22+ with method patterns), no JSON streaming libraries, no SSE libraries.

---

## Testing Strategy

| Component | Test Type | Mechanism |
|---|---|---|
| `provider/openai.go` | Unit | Feed recorded SSE lines to `ParseEvent`, assert `Chunk` values |
| `provider/anthropic.go` | Unit | Feed recorded SSE events to `ParseEvent`, assert `Chunk` values |
| `stream/streamer.go` | Integration | `httptest.Server` as mock upstream, verify chunk output and cancellation |
| `handler.go` | Integration | `httptest.Server` + mock provider, verify HTTP responses and SSE output |

All tests run with `go test ./...`, no setup required, no network calls.
