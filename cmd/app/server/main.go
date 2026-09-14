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
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/internal/harness"
	"github.com/sp4aceman/ai-mud/internal/httpapi"
	sshserver "github.com/sp4aceman/ai-mud/internal/server/ssh"
	"github.com/sp4aceman/ai-mud/internal/storage"
)

func main() {
	// log to stderr with a simple text format for now
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// context cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gameServer := game.NewServer()

	// storage is infrastructure: with docker compose the healthchecks
	// start the two postgres containers first, and Connect retries
	// while they boot.
	// Failing to connect takes the server down (unlike optional
	// components such as the harness).
	store, err := storage.Connect(ctx, storage.ConfigFromEnv())
	if err != nil {
		slog.Error("storage unavailable", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	// worlds: when an active persisted world exists it REPLACES the
	// seed-env world — the loader replays terrain (from the record's
	// seed) and authored content (world_objects). No persisted world →
	// the classic W_SEED flow (a re-activated world shows up on restart).
	if w, err := store.Relational.ActiveWorld(ctx); err == nil {
		objs, err2 := store.Relational.ListObjects(ctx, w.ID)
		if err2 != nil {
			slog.Warn("persisted world content unreachable, using seed flow", "err", err2)
		} else {
			gameServer = game.NewServerWorld(game.WorldSpec{
				ID:      w.ID,
				Name:    w.Name,
				Seed:    w.Seed,
				WW:      w.WW,
				WH:      w.WH,
				SpawnX:  w.SpawnX,
				SpawnY:  w.SpawnY,
				Objects: objs,
			})
			gameServer.AttachWorldStore(store.Relational, w.ID)
			if doc, ok := store.Document.(*storage.Document); ok {
				gameServer.AttachNarrStore(doc)
			}
		}
	} else if !errors.Is(err, storage.ErrNotFound) {
		slog.Warn("active world lookup failed, using seed flow", "err", err)
	}

	// accounts live in the document backend; nil Document → memory mode
	accts := auth.NewAccounts(store.Document)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return sshserver.Run(ctx, sshserver.DefaultConfig(), gameServer, accts)
	})
	g.Go(func() error {
		return httpapi.Run(ctx, httpapi.DefaultConfig(), gameServer)
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
