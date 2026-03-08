package heimdall

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// StreamFunc is the signature of the streaming function injected into ChatHandler.
// In production this is stream.Stream; in tests it can be a mock.
type StreamFunc func(ctx context.Context, w http.ResponseWriter, provider Provider, req ChatRequest) error

// ChatHandler returns an http.HandlerFunc that validates the request, resolves
// the provider, sets SSE headers, and delegates to streamFn.
func ChatHandler(providers map[string]Provider, streamFn StreamFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		
		// decode the JSON body into ChatRequest
		// if JSON malformed, return 400
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Provider == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider is required"})
			return
		}
		if req.Model == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model is required"})
			return
		}
		if len(req.Messages) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "messages is required"})
			return
		}
		p, ok := providers[req.Provider]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown provider: %s", req.Provider)})
			return
		}

		// once Write or WriteHeader is called, we are locked in SSE.
		// we cannot send http 400 or 500 from this point on.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// call the stream function with the client's context, the response writer, the provider and the request.
		if err := streamFn(r.Context(), w, p, req); err != nil {
			// Pre-stream failure: no bytes written yet, safe to send a proper HTTP error.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
