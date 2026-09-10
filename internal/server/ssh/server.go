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
func Run(ctx context.Context, cfg Config, world *game.Server, accts *auth.Accounts) error {
	if accts == nil {
		accts = auth.NewAccounts(nil) // memory mode — dev/hosts without a DB
	}

	s, err := wish.NewServer(
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		// any public key is accepted — possession IS identity here:
		// the fingerprint fronts a new account on first sighting.
		// An allowlist would reject brand-new players.
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			bm.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				return teaSession(accts, world, s)
			}),
			logging.Middleware(),
		),
	)
	if err != nil {
		return err
	}
	// keep key-less connections too ("either or": pubkey OR none):
	// NoClientAuth=true admits anonymous sessions while the pubkey
	// handler still verifies keys that present one. Set post-
	// construction because charm's ssh derives NoClientAuth only when
	// NO auth handler exists.
	// NOTE(perf-bisect): ServerConfigCallback(NoClientAuth) disabled —
	// suspect it stalls the bubbletea cmd pipeline.
	// s.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
	// 	return &gossh.ServerConfig{NoClientAuth: true}
	// }
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
// key fingerprint, resolve (or mint) the account behind it, and hand
// the identity plus the world client to the screen router.
func teaSession(accts *auth.Accounts, world *game.Server, s ssh.Session) (tea.Model, []tea.ProgramOption) {
	pty, _, active := s.Pty()
	if !active {
		wish.Fatalln(s, "no active terminal, refusing to start")
		return nil, nil
	}

	fp := auth.Fingerprint(s.PublicKey())
	identity, err := accts.Identify(fp)
	if err != nil {
		slog.Error("auth failed for session", "err", err)
		wish.Fatalln(s, "authentication failed")
		return nil, nil
	}
	slog.Info("session connected",
		"user", identity.User.Name,
		"fingerprint", fp,
		"fresh", identity.AccountFresh,
		"term", pty.Term)

	return ui.NewRouterWithAccounts(identity, world, accts), nil
}

// ensure host key directory exists before wish tries to write the key
func init() {
	if err := os.MkdirAll(".ssh", 0o700); err != nil {
		log.Warn("could not create .ssh dir", "err", err)
	}
}
