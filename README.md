```
 _   _ _____ ___ __  __ ____    _    _     _
| | | | ____|_ _|  \/  |  _ \  / \  | |   | |
| |_| |  _|  | || |\/| | | | |/ _ \ | |   | |
|  _  | |___ | || |  | | |_| / ___ \| |___| |___
|_| |_|_____|___|_|  |_|____/_/   \_\_____|_____|
```

# heimdall

Heimdall is a minimal LLM streaming proxy written in Go, built as a learning
project to explore streaming HTTP, interface-driven design, and production
server patterns without any external dependencies. It accepts a unified chat
request, translates it to the target provider's wire format, and streams the
response back to the client as normalized Server-Sent Events in real time.

---

## How it works

A client sends one request to `/chat` with a provider name, a model, and a
list of messages. Heimdall translates that into the correct upstream API
format (OpenAI or Anthropic), opens a streaming connection, and forwards each
token back to the client as a JSON SSE event as it arrives. The client sees
tokens in real time rather than waiting for the full response.

Every provider speaks a different streaming protocol. Heimdall normalizes all
of them into a single `Chunk` type before they reach the network layer. No
provider-specific types ever leave their adapter file.

---

## Project structure

```
heimdall/
├── heimdall.go                 canonical types and the Provider interface
├── handler.go                  HTTP handler: validation, SSE headers, routing
├── cmd/
│   └── heimdall/
│       └── main.go             server entry point, env config, graceful shutdown
├── stream/
│   └── streamer.go             SSE parse loop, flush, TTFT, disconnect detection
└── provider/
    ├── openai.go               OpenAI adapter
    └── anthropic.go            Anthropic adapter
```

### heimdall.go

The contract everything else is built against. Defines `ChatRequest`,
`Message`, `Chunk`, `Usage`, `Metrics`, and the `Provider` interface. Zero
logic, minimal imports. If you want to understand the system, start here.

```go
type Provider interface {
    BuildRequest(ctx context.Context, req ChatRequest) (*http.Request, error)
    ParseEvent(eventType, data string) (*Chunk, error)
}
```

Adding a new provider means implementing these two methods and registering the
adapter in `main.go`. Nothing else changes.

### handler.go

Decodes and validates the incoming `ChatRequest`, looks up the provider from
the registry, sets SSE response headers, and delegates to the stream function.
All pre-stream errors (bad input, unknown provider) return normal HTTP
responses. Once the first byte is written to the client, the protocol is
locked to SSE and errors are delivered as a final chunk.

### stream/streamer.go

The core of the proxy. Builds the upstream request, opens the connection,
reads raw SSE line by line, calls `provider.ParseEvent` for each complete
event, and writes the normalized chunk back to the client with an immediate
flush after every token. Also tracks time-to-first-token (TTFT) and handles
client disconnects by cancelling the upstream request through context.

### provider/openai.go and provider/anthropic.go

Each file implements the `Provider` interface for one upstream API.
`BuildRequest` constructs the correct HTTP request. `ParseEvent` extracts
token content, usage, and done signals from the provider's SSE format.

OpenAI uses untyped `data:` lines with a `[DONE]` sentinel.
Anthropic uses typed events (`event: content_block_delta`, `event:
message_stop`, etc.). The streamer handles raw SSE framing and passes both the
event type and data payload to the provider — OpenAI ignores the type, Anthropic
switches on it.

---

## Getting started

### Prerequisites

- Go 1.21 or later
- An OpenAI or Anthropic API key (or both)
- `air` for hot reload in development: `go install github.com/air-verse/air@latest`

### Environment variables

| Variable            | Required        | Default | Description         |
|---------------------|-----------------|---------|---------------------|
| `OPENAI_API_KEY`    | For OpenAI      | —       | OpenAI API key      |
| `ANTHROPIC_API_KEY` | For Anthropic   | —       | Anthropic API key   |
| `PORT`              | No              | `8080`  | Server listen port  |

Set at least one API key before starting:

```sh
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
```

### Running

```sh
make dev       # hot reload with air (recommended during development)
make run       # single run with go run
make build     # compile to bin/heimdall
./bin/heimdall # run the binary directly
```

---

## API

### POST /chat

Send a chat request to any configured provider.

```sh
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "openai",
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

**Request body**

| Field      | Type              | Required | Description                      |
|------------|-------------------|----------|----------------------------------|
| `provider` | string            | yes      | `"openai"` or `"anthropic"`      |
| `model`    | string            | yes      | Model name for the provider      |
| `messages` | array of messages | yes      | Conversation history             |

Each message has a `role` (`"user"`, `"assistant"`, or `"system"`) and a
`content` string.

**Response — SSE stream**

```
data: {"content":"Hello","done":false}

data: {"content":"!","done":false}

data: {"content":"","done":true,"usage":{"input_tokens":8,"output_tokens":9},"metrics":{"ttft_ms":83,"total_ms":210}}
```

The final chunk always has `"done": true` and carries token usage and timing
metrics. If an error occurs mid-stream it is delivered as a final chunk with
an `"error"` field and the connection closes.

**Pre-stream errors** return standard HTTP responses:
- `400` — missing or invalid fields, unknown provider
- `502` — upstream connection failure

### GET /health

Returns `200 OK`. No body. Use this for liveness checks.

---

## Make targets

```
make           show this help
make dev       run with hot reload (requires air)
make run       run with go run
make build     compile to bin/heimdall
make test      run all tests
make test/v    run all tests verbose
make clean     remove bin/
```

---

## Testing

Tests use `net/http/httptest` servers that replay recorded SSE responses. No
real API calls are made. Each provider is tested end-to-end: raw SSE bytes in,
normalized chunks out. The streamer is tested with a mock provider to verify
flush behavior, TTFT measurement, and disconnect handling.

```sh
make test
```

