package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jerkeyray/heimdall"
	"github.com/jerkeyray/heimdall/provider"
	"github.com/jerkeyray/heimdall/stream"
)

func main() {
	port := envOr("PORT", "8080")

	// read API keys from ENV and build provider registry.
	providers := make(map[string]heimdall.Provider)
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		providers["openai"] = &provider.OpenAI{APIKey: key}
		slog.Info("provider registered", "name", "openai")
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		providers["anthropic"] = &provider.Anthropic{APIKey: key}
		slog.Info("provider registered", "name", "anthropic")
	}
	if len(providers) == 0 {
		slog.Error("no providers configured: set OPENAI_API_KEY or ANTHROPIC_API_KEY")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat", heimdall.ChatHandler(providers, stream.Stream))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // how long to wait for client to send request headers.
		IdleTimeout:       120 * time.Second, // how long a keep alive connection can sit idle between requests
		// ReadTimeout and WriteTimeout are intentionally 0: streaming responses
		// can run for minutes, so per-request timeouts are handled by the streamer.
	}

	go func() {
		slog.Info("server starting", "addr", srv.Addr)
		// check for http.ErrServerClosed else every clean shutdown would log as an error and call os.Exit(1)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// graceful shutdown
	// SIGINT -> ctrl + c and SIGTERM -> sent by docker and other process managers to stop process
	// signal.Notify sends these signals to quit channel instead of just killing the process.
	// buffer size of 1 because if the signal arrived before the <-quit was reached, the signal would be dropped and 
	// process would never shut down.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM) 
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// srv.Shutdown stops accepting incoming connections and waits for all in-flight requests to finish naturally.
	// context gives us a deadline (30s) because active streaming responses can be mid-token when shutdown is triggered 
	// and we need to give those streams time to send their final chunk cleanly before the process dies.
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
