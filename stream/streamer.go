package stream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jerkeyray/heimdall"
)

// package level HTTP client with a 10 second timeout
// 10 sec timeout is for establishing the TCP connection (not the stream)
// shared across all requests for connection pooling
var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,
	},
}

// wireChunk is the JSON shape written to the client for each SSE event.
type wireChunk struct {
	Content string       `json:"content"`
	Done    bool         `json:"done"`
	Error   string       `json:"error,omitempty"` // string instead of error since error interface doesn't serialize to JSON
	Usage   *wireUsage   `json:"usage,omitempty"`
	Metrics *wireMetrics `json:"metrics,omitempty"`
}

type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type wireMetrics struct {
	TTFTMs  int64 `json:"ttft_ms"`
	TotalMs int64 `json:"total_ms"`
}

// Stream executes the full lifecycle of a single proxied request:
// build upstream request → read SSE → normalize → flush to client.
func Stream(ctx context.Context, w http.ResponseWriter, provider heimdall.Provider, req heimdall.ChatRequest) error {
	// check if ResponseWriter supports flushing.
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported: ResponseWriter does not implement http.Flusher")
	}

	// build the provider specific request
	// ctx forms the cancellation chain: 
	// client get cancelled -> ctx gets cancelled -> upstreamReq gets cancelled -> stream loop exits
	upstreamReq, err := provider.BuildRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	// pre-stream error check
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// SSE parse loop 
	start := time.Now() // request start time for TTFT and Total time calculation
	var ttft time.Duration // stays zero until first token arrives
	var accUsage heimdall.Usage // accumulates tokens

	body := &idleTimeoutReader{r: resp.Body, timeout: 30 * time.Second}
	scanner := bufio.NewScanner(body)

	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		// blank line = end of SSE event, reset event type.
		if line == "" {
			eventType = ""
			continue
		}

		// comment or keepalive ping.
		if strings.HasPrefix(line, ":") {
			continue
		}

		// prefix - store data type for next data line
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		// unknown field - skip
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		// strip prefix, save payload
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		// hand the eventType to the provider adapter, if it returns an error, inject an error in the stream, since we can't send a 500 anymore.
		chunk, err := provider.ParseEvent(eventType, data)
		if err != nil {
			writeErrorChunk(w, flusher, err.Error())
			return nil
		}

		if chunk == nil {
			continue
		}

		// accumulate usage because anthropic splits it across two events.
		if chunk.Usage != nil {
			if chunk.Usage.InputTokens > 0 {
				accUsage.InputTokens = chunk.Usage.InputTokens
			}
			if chunk.Usage.OutputTokens > 0 {
				accUsage.OutputTokens = chunk.Usage.OutputTokens
			}
		}

		// mid-stream provider error.
		if chunk.Err != nil {
			writeErrorChunk(w, flusher, chunk.Err.Error())
			return nil
		}

		// record TTFT on first chunk that carries content.
		if ttft == 0 && chunk.Content != "" {
			ttft = time.Since(start)
		}

		// log total duration, log info, write final chunk, return.
		if chunk.Done {
			total := time.Since(start)
			slog.Info("stream complete",
				"provider", req.Provider,
				"model", req.Model,
				"ttft_ms", ttft.Milliseconds(),
				"total_ms", total.Milliseconds(),
				"input_tokens", accUsage.InputTokens,
				"output_tokens", accUsage.OutputTokens,
			)
			writeChunk(w, flusher, wireChunk{
				Done: true,
				Usage: &wireUsage{
					InputTokens:  accUsage.InputTokens,
					OutputTokens: accUsage.OutputTokens,
				},
				Metrics: &wireMetrics{
					TTFTMs:  ttft.Milliseconds(),
					TotalMs: total.Milliseconds(),
				},
			})
			return nil
		}

		// Normal token chunk.
		writeChunk(w, flusher, wireChunk{Content: chunk.Content})
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			slog.Info("client disconnected", "provider", req.Provider, "model", req.Model)
			return nil
		}
		writeErrorChunk(w, flusher, fmt.Sprintf("stream read error: %s", err.Error()))
		return nil
	}

	// EOF without a Done chunk - stream ended unexpectedly.
	writeErrorChunk(w, flusher, "stream ended without completion signal")
	return nil
}

// every write goes through here
func writeChunk(w http.ResponseWriter, f http.Flusher, c wireChunk) {
	b, _ := json.Marshal(c)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush() // flush after every single write else go buffers the write and client sees nothing until buffer fills.
}

func writeErrorChunk(w http.ResponseWriter, f http.Flusher, msg string) {
	writeChunk(w, f, wireChunk{Done: true, Error: msg})
}

// idleTimeoutReader closes the underlying reader if no data arrives within timeout.
type idleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	if r.timer == nil {
		r.timer = time.AfterFunc(r.timeout, func() { r.r.Close() })
	} else {
		r.timer.Reset(r.timeout)
	}
	return r.r.Read(p)
}
