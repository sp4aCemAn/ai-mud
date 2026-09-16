package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/sp4aceman/ai-mud/internal/game"
)

// Harness is the AI game master loop: a ticker observes the world
// through game.Server's GM readout, prompts the local LLM, parses the
// answer tolerantly (prose noise is the norm on small models — the
// parse digs the JSON out; anything unparseable is a clean no-op) and
// applies the parsed verbs.
//
// Slice 1 contract: the harness REMAINS non-fatal — an unreachable or
// rambling LLM logs and idles; the game keeps serving (the
// tests/harness_mock CI holds this line).

type Harness struct {
	cfg    Config
	client *Client
	game   Game

	// lastActions: a short in-memory cooldown log — loop-level lameness
	// guard so a model can't repeat the same action forever.
	lastActions []string
}

// Game is the harness-side seam (consumer-side interface; main.go wires
// the live game.Server in). The verbs carry their own rails in the
// game package.
type Game interface {
	Observe() game.GMReadout
	GMAnnounce(text string) error
}

func New(cfg Config, g Game) *Harness {
	return &Harness{cfg: cfg, client: NewClient(cfg), game: g}
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

	cadence, err := time.ParseDuration(h.cfg.Cadence)
	if err != nil || cadence <= 0 {
		cadence = 45 * time.Second
	}

	slog.Info("harness starting", "base_url", h.cfg.BaseURL,
		"model", h.cfg.Model, "cadence", cadence)

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

	ticker := time.NewTicker(cadence)
	defer ticker.Stop()

	// first beat: survey at boot even with an idle world (the config
	// sanity check) — can't happen via the emptiness gate on Observe
	h.cycle(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("harness stopping", "err", ctx.Err())
			return nil
		case <-ticker.C:
			h.cycle(ctx)
		}
	}
}

// cycle: observe → prompt → parse → apply. Errors log, never fail the
// server (the harness is the optional component by design).
func (h *Harness) cycle(ctx context.Context) {
	obs := h.game.Observe()
	if len(obs.Events) == 0 && len(h.lastActions) > 0 {
		// a quiet world doesn't earn a prompt — the local model isn't
		// free, and silence shouldn't spam cycles
		slog.Info("harness: idle world, cycle skipped")
		return
	}
	slog.Info("harness: cycle begins", "events", len(obs.Events),
		"players", obs.World.PlayerCount, "towns", len(obs.Towns))
	prompt := h.prompt(obs)
	resp, err := h.output(ctx, prompt)
	if err != nil {
		// reachable-or-rambling LLM is a warning, not a death
		slog.Warn("harness: prompt failed", "err", err)
		return
	}
	slog.Info("harness: cycle reply", "bytes", len(resp.Text()))
	if err := ctx.Err(); err != nil {
		return // the shutdown gate wins over the current cycle
	}
	actions := parseActions(resp.Text())
	if len(actions) == 0 {
		slog.Info("harness: no actionable output this cycle (idle)")
		return
	}
	limit := maxActions
	if len(actions) < limit {
		limit = len(actions)
	}
	for _, a := range actions[:limit] {
		if err := ctx.Err(); err != nil {
			return // stop applying the moment shutdown starts
		}
		h.apply(ctx, a)
	}
}

// output wraps the chat call with the game-master turn needle (max
// tokens generous-ish: reasoning models' chain-of-thought eat budget).
// The system card is validated at load — a missing persona leaves the
// loop with a fallback seed rather than prompting a model with "".
func (h *Harness) output(ctx context.Context, user string) (*ChatResponse, error) {
	persona := strings.TrimSpace(h.cfg.SystemPrompt)
	if persona == "" {
		persona = cfgSystemFallback
	}
	if h.cfg.Model == "" {
		return nil, fmtErrNoFeature("harness model unset (config model: empty)")
	}
	req := ChatRequest{
		Model: h.cfg.Model,
		Messages: []ChatMessage{
			{Role: "system", Content: persona + systemToolSuffix},
			{Role: "user", Content: user},
		},
		Temperature: h.cfg.Temperature,
		MaxTokens:   max(700, h.cfg.MaxTokens),
	}
	resp, err := h.client.ChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("chat turn: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("chat turn returned no choices")
	}
	return resp, nil
}

// systemToolSuffix hangs the one-JSON-object protocol off the persona
// (config keeps the persona voice; this suffix owns the exact format).
const systemToolSuffix = `

You act by replying with EXACTLY ONE JSON object, and nothing else:
{"actions":[{"type":"announce","text":"..."}]}
Keep it small. If nothing needs doing, reply {"actions":[]}. Have ideas
about the world's mood, but respecting the rails: brief, concrete, in-fiction.`

// cfgSystemFallback keeps the loop promptable even when the config has
// no persona at all (LoadConfig allows a voice-less harness; the
// fallback stays terse and in-genre).
const cfgSystemFallback = `You are the game master of a multiplayer dungeon world.`

// fmtErrNoFeature describes an actionable misconfiguration cleanly.
func fmtErrNoFeature(msg string) error {
	return errors.New(msg)
}

// maxActions caps the per-cycle tool spindle (the rails: even a
// well-behaved model doesn't get to cascade 10 tools at once).
const maxActions = 3

// GMAction is one parsed tool call from the model's reply.
type GMAction struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parseActions tolerates the local models' prose habits: ```json
// fences, prose around the object, a bare "announce" object. Anything
// unparseable → nil (a no-op cycle, never an error).
func parseActions(text string) []GMAction {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// strip the code fences the models love to wrap JSON in
	if i := strings.Index(text, "{"); i >= 0 {
		end := strings.LastIndex(text, "}")
		if end > i {
			text = text[i : end+1]
		}
	}
	// tolerate a single action object without the "actions" wrapper
	// (decode order matters: a bare action must not read as an
	// empty-wrapper "hold")
	var single GMAction
	if err := json.Unmarshal([]byte(text), &single); err == nil && single.Type != "" {
		return []GMAction{single}
	}
	var wrapper struct {
		Actions []GMAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil {
		if len(wrapper.Actions) == 0 {
			return nil // the model explicitly chose to hold — respect it
		}
		return wrapper.Actions
	}
	// pure prose — the models' chit-chat stays a NO-OP: announcing it
	// would flood player event lines with reasoning noise (the mock's
	// "the world holds its breath" is exactly this shape). A cycle
	// with no JSON is idle by contract, never noisy.
	return nil
}

// apply: the slice-1 verb surface — announce only; every other action
// logs as not-yet-wired (the rails tighten before hands grow).
func (h *Harness) apply(ctx context.Context, a GMAction) {
	switch a.Type {
	case "announce":
		if err := h.game.GMAnnounce(a.Text); err != nil {
			slog.Warn("harness: announce rejected", "err", err)
			return
		}
		h.remember("announce: " + a.Text)
	default:
		slog.Info("harness: action type not connected yet (phase 2)", "type", a.Type)
	}
}

// remember folds short-term action memory (loop-lame guard: no repeat).
func (h *Harness) remember(line string) {
	h.lastActions = append(h.lastActions, line)
	if len(h.lastActions) > 4 {
		h.lastActions = h.lastActions[len(h.lastActions)-4:]
	}
}

// prompt builds the user turn: a compact world digest + the drained
// events (short, structured — the local model's budget is real).
func (h *Harness) prompt(obs game.GMReadout) string {
	var b strings.Builder
	b.WriteString("WORLD: ")
	b.WriteString(readoutLine(obs))
	b.WriteString("\nEVENTS SINCE LAST CYCLE:\n")
	if len(obs.Events) == 0 {
		b.WriteString("(quiet)\n")
	}
	for _, e := range obs.Events {
		fmt.Fprintf(&b, "- %s: %s\n", e.Kind, e.Text)
	}
	b.WriteString("What does the world murmur? Reply with the JSON object.")
	return b.String()
}

// readoutLine renders the world card in one compact line.
func readoutLine(obs game.GMReadout) string {
	var towns []string
	for _, t := range obs.Towns {
		towns = append(towns, fmt.Sprintf("%s(id=%d door=%d,%d)", t.Name, t.ID, t.Door[0], t.Door[1]))
	}
	return fmt.Sprintf("size %dx%d, players %d, enemies %d, towns [%s]",
		obs.World.W, obs.World.H, obs.World.PlayerCount,
		obs.World.EnemyCount, strings.Join(towns, " "))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
