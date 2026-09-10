// Package sshserver implements the SSH compositor: each SSH connection
// becomes a bubbletea session rendering the game UI.
package sshserver

import (
	"context"
	"encoding/base64"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	gossh "golang.org/x/crypto/ssh"
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
	// either-or lanes for one negotiated auth sequence: a bare
	// "none" accept would END the handshake (the client stops before
	// ever offering a key), so NoClientAuthCallback answers the none
	// probe with partial success and re-advertises the two real
	// lanes: publickey (possession is identity) and
	// keyboard-interactive (press-enter guest).
	s.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{
			NoClientAuth: true,
			NoClientAuthCallback: func(gossh.ConnMetadata) (*gossh.Permissions, error) {
				return nil, &gossh.PartialSuccessError{
					Next: gossh.ServerAuthCallbacks{
						PublicKeyCallback:           publicKeyLane,
						KeyboardInteractiveCallback: guestLane,
					},
				}
			},
			MaxAuthTries: 10,
		}
	}
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

// charmPubKeyExt is charm's internal permissions key for the
// authenticated public key (privacy leak in the library's unexported
// var, so the literal is mirrored here) — wish re-parses it after
// authentication to make s.PublicKey() work.
const charmPubKeyExt = "gliderlabs/ssh.PublicKey"

// publicKeyLane is the keyed lane: any key proves identity
// (possession IS identity; the fingerprint fronts an account).
// Its permissions carry the marshaled key in charm's extension slot.
func publicKeyLane(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
	return &gossh.Permissions{
		Extensions: map[string]string{
			charmPubKeyExt: base64.StdEncoding.EncodeToString(key.Marshal()),
		},
	}, nil
}

// guestLane is the keyless lane: one empty reply joins as a guest.
func guestLane(_ gossh.ConnMetadata, challenger gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
	_, err := challenger(
		"no ssh key offered",
		"connecting without a key plays as a guest — press enter to continue",
		[]string{"user name"},
		[]bool{false},
	)
	if err != nil {
		return nil, err
	}
	return &gossh.Permissions{}, nil
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

	// altscreen + capped FPS: the renderer holds a framebuffer and
	// only writes changed lines per frame, and the 30fps ceiling
	// halves the bytes a fast typewriter generates. This is the
	// keystroke-lag fix, not polish.
	return ui.NewRouterWithAccounts(identity, world, accts), []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithFPS(30),
	}
}

// ensure host key directory exists before wish tries to write the key
func init() {
	if err := os.MkdirAll(".ssh", 0o700); err != nil {
		log.Warn("could not create .ssh dir", "err", err)
	}
}
