// Package sshserver implements the SSH compositor: each SSH connection
// becomes a bubbletea session rendering the game UI.
package sshserver

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/game"
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
func Run(ctx context.Context, cfg Config, world *game.Server) error {
	// TODO: swap for a real provider (SSH PublicKeyHandler + database)
	// once auth design settles; every client is the same guest for now.
	provider := auth.NewGuestProvider()

	s, err := wish.NewServer(
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithMiddleware(
			bm.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				return teaSession(provider, world, s)
			}),
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

// teaSession runs the connect sequence for one SSH session: derive the
// key fingerprint, run the (currently templated) auth flow, and hand the
// resulting identity plus the world client to the screen router.
func teaSession(provider auth.Provider, world *game.Server, s ssh.Session) (tea.Model, []tea.ProgramOption) {
	pty, _, active := s.Pty()
	if !active {
		wish.Fatalln(s, "no active terminal, refusing to start")
		return nil, nil
	}

	fp := auth.Fingerprint(s.PublicKey())
	identity, err := auth.Identify(provider, fp)
	if err != nil {
		slog.Error("auth failed for session", "err", err)
		wish.Fatalln(s, "authentication failed")
		return nil, nil
	}
	slog.Info("session connected",
		"user", identity.User.Name,
		"fingerprint", fp,
		"term", pty.Term)

	return ui.NewRouter(identity, world), nil
}

// ensure host key directory exists before wish tries to write the key
func init() {
	if err := os.MkdirAll(".ssh", 0o700); err != nil {
		log.Warn("could not create .ssh dir", "err", err)
	}
}
