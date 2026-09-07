// ai-mud server entrypoint.
//
// The service is composed of long-running components, each run as a
// goroutine managed by an errgroup:
//
//   - SSH compositor (internal/server/ssh): players connect over SSH
//   - HTTP API (internal/httpapi): web interface + health endpoints
//   - Game server (internal/game): world state and the game tick loop
//   - AI harness (TBD): the "game master" that generates/steers the world
//
// Any component returning an error cancels the shared context, shutting
// down the rest. Ctrl-C triggers a graceful shutdown.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/internal/harness"
	"github.com/sp4aceman/ai-mud/internal/httpapi"
	sshserver "github.com/sp4aceman/ai-mud/internal/server/ssh"
)

func main() {
	// log to stderr with a simple text format for now
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// context cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gameServer := game.NewServer()

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return sshserver.Run(ctx, sshserver.DefaultConfig(), gameServer)
	})
	g.Go(func() error {
		return httpapi.Run(ctx, httpapi.DefaultConfig())
	})
	g.Go(func() error {
		return gameServer.Run(ctx)
	})

	// AI harness — component is skipped (with a warning) when the config
	// file is missing; it never takes the server down if the LLM is off.
	if hCfg, err := harness.LoadConfig(harness.ConfigPath()); err != nil {
		slog.Warn("harness disabled", "err", err)
	} else {
		g.Go(func() error { return harness.New(hCfg).Run(ctx) })
	}

	slog.Info("ai-mud starting")
	if err := g.Wait(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
	slog.Info("ai-mud stopped cleanly")
}
