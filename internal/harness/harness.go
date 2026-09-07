// Package harness is the AI "game master": it prompts an LLM to
// hallucinate/steer the game world and keeps track of events.
//
// Current state: skeleton. It loads config, builds an OpenAI-compatible
// client, verifies the endpoint at startup, and then idles. The actual
// game-master loop (observe game events → prompt LLM → apply resulting
// events to the game server) is TBD.
package harness

import (
	"context"
	"log/slog"
)

type Harness struct {
	cfg    Config
	client *Client
}

func New(cfg Config) *Harness {
	return &Harness{cfg: cfg, client: NewClient(cfg)}
}

// Run is the harness component loop. It never returns an error unless
// something truly fatal happens — an unreachable LLM server is logged as
// a warning and the harness idles, so a missing/broken local server does
// not take down the whole game server.
func (h *Harness) Run(ctx context.Context) error {
	if !h.cfg.Enabled {
		slog.Info("harness disabled in config, skipping")
		return nil
	}

	slog.Info("harness starting", "base_url", h.cfg.BaseURL, "model", h.cfg.Model)

	models, err := h.client.Models(ctx)
	switch {
	case err != nil:
		slog.Warn("harness: LLM endpoint unreachable, idling (start your local server and restart)",
			"err", err)
	case len(models) == 0:
		slog.Warn("harness: endpoint reachable but no models available")
	default:
		slog.Info("harness: endpoint reachable", "models", models)
	}

	// TODO: game-master loop:
	//   - subscribe to game events (world state, player actions)
	//   - build prompts (system prompt from config + event context)
	//   - call ChatCompletion / Responses
	//   - parse structured output into world events
	//   - emit events back into the game server
	<-ctx.Done()
	slog.Info("harness stopping", "err", ctx.Err())
	return nil
}
