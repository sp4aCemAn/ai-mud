// Package sshserver implements the SSH compositor: each SSH connection
// becomes a bubbletea session rendering the game UI.
package sshserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	"github.com/sp4aceman/ai-mud/internal/ui"
)

type Config struct {
	Addr        string
	HostKeyPath string
}

// TODO: make ports env/flag configurable — 2525 is temporary (2222 blocked)
func DefaultConfig() Config {
	return Config{
		Addr:        "0.0.0.0:2525",
		HostKeyPath: ".ssh/ai-mud_host_key",
	}
}

// Run starts the SSH server and blocks until ctx is cancelled or a
// fatal error occurs. It shuts down gracefully on ctx cancellation.
func Run(ctx context.Context, cfg Config) error {
	s, err := wish.NewServer(
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithMiddleware(
			bm.Middleware(teaHandler),
			logging.Middleware(),
		),
	)
	if err != nil {
		return err
	}

	// graceful shutdown when the parent context is cancelled
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Error("ssh shutdown", "err", err)
		}
		close(done)
	}()

	slog.Info("ssh server listening", "addr", cfg.Addr)
	err = s.ListenAndServe()
	if err != nil && !errors.Is(err, ssh.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	<-done
	return nil
}

// teaHandler returns the bubbletea model to render for a new SSH session.
// Later this is where we authenticate / attach the session to a player.
func teaHandler(s ssh.Session) (tea.Model, []tea.ProgramOption) {
	pty, _, active := s.Pty()
	if !active {
		wish.Fatalln(s, "no active terminal, refusing to start")
		return nil, nil
	}
	_ = pty
	return ui.InitalScreen(), nil
}

// ensure host key directory exists before wish tries to write the key
func init() {
	if err := os.MkdirAll(".ssh", 0o700); err != nil {
		log.Warn("could not create .ssh dir", "err", err)
	}
}
