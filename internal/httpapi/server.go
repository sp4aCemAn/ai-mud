// Package httpapi is the web interface for the MUD.
// Health/metadata endpoints plus the tool-call surface the AI harness
// and operators drive: spawn/despawn world content, announce.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/sp4aceman/ai-mud/internal/game"
)

type Config struct {
	Addr string
}

// TODO: make ports env/flag configurable — 8081 is temporary (8080 blocked)
func DefaultConfig() Config {
	return Config{Addr: "0.0.0.0:8081"}
}

// NewRouter serves: /healthz plus the tool routes. The game server is
// injected (nil → health only; tool calls need a live world).
func NewRouter(gs *game.Server) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	if gs != nil {
		mux.Handle("/api/", toolRoutes(gs))
	}

	return mux
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config, gs *game.Server) error {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           NewRouter(gs),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("http shutdown", "err", err)
		}
		close(done)
	}()

	slog.Info("http server listening", "addr", cfg.Addr, "tools", gs != nil)
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	<-done
	return nil
}
