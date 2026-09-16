package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/internal/storage"
)

// The game-master loop, two stages (cactus integration):
//
//	STAGE 1 — the PERSONA turn: lore, mood, a decision in natural
//	          language. The verb card is NEVER attached (the writer
//	          never becomes the interpreter).
//	STAGE 2 — the INTERPRETER turn: the same endpoint, the verb card
//	          attached; native tool_calls come back flat and simple.
//
// The turn TRIGGER is the frontier door: the game signals fresh
// exploration (players push into unexplored country), and the cadence
// tick stays only as the floor.
//
// Contract: the harness is the optional component — an unreachable or
// rambling LLM logs and idles; the game keeps serving (the
// tests/harness_mock CI holds that line). With no verb card the loop
// degrades to the single-stage text protocol (LM-Studio-style backends).

type Harness struct {
	cfg Config
	// client + iclient: the persona's provider and the interpreter's
	// provider are SEPARATE (cactus exists to transform decisions into
	// tool calls; LM Studio's gemma owns the lore). Same endpoint when
	// only one backend is around.
	client   *Client
	iclient  *Client
	game     Game
	recorder GenerationRecorder // the turn ledger (nil = skipped)

	tools []ToolDef // the verb card (nil = single-stage legacy mode)

	// turnInFlight: the queue's coalescing gate — one model turn at a
	// time; a poke arriving mid-turn folds into the next batch (the
	// game's ring holds the events; nothing is ever dropped)
	turnInFlight atomic.Bool
	// lastTurn + cooldown: the frontier door can stampede (each fresh
	// tile a deep explorer crosses pokes the queue) — after a turn, the
	// door stays quiet for the cooldown; stragglers fold into the next
	// beat (the ring holds their events; nothing is dropped)
	cooldown    time.Duration
	lastTurn    time.Time
	lastActions []string
}

// GenerationRecorder is the audit-trail seam (writes one row per GM
// turn; adapted from *storage.Relational — main.go wires it).
type GenerationRecorder interface {
	RecordGeneration(ctx context.Context, g storage.Generation) (storage.Generation, error)
}

// RelationalGeneration adapts the storage side to the harness seam
// (the generations table is the gm-cycle ledger of record).
type RelationalGeneration struct{ Ref GenerationSaver }

type GenerationSaver interface {
	SaveGeneration(ctx context.Context, g storage.Generation) (storage.Generation, error)
}

func (r RelationalGeneration) RecordGeneration(ctx context.Context, g storage.Generation) (storage.Generation, error) {
	return r.Ref.SaveGeneration(ctx, g)
}

// Game is the harness-side seam (consumer-side interface; main.go wires
// the live game.Server in). The verbs carry their own rails in the
// game package.
type Game interface {
	Observe() game.GMReadout
	GMAnnounce(text string) error
	GMSpawnEnemies(count, level int) error
	GMRaiseVillage(name string) error
	Poke() <-chan struct{}
}

func New(cfg Config, g Game) *Harness {
	h := &Harness{
		cfg: cfg, client: NewClient(cfg), game: g,
		cooldown: turnCooldown(cfg.Cooldown),
	}
	if cfg.InterpreterBaseURL != "" || cfg.InterpreterModel != "" {
		icfg := cfg
		if cfg.InterpreterBaseURL != "" {
			icfg.BaseURL = cfg.InterpreterBaseURL
		}
		if cfg.InterpreterModel != "" {
			icfg.Model = cfg.InterpreterModel
		}
		h.iclient = NewClient(icfg)
		slog.Info("harness: separate interpreter provider",
			"persona", fmt.Sprintf("%s@%s", cfg.Model, cfg.BaseURL),
			"interpreter", fmt.Sprintf("%s@%s", icfg.Model, icfg.BaseURL))
	}
	h.loadTools()
	return h
}

// turnCooldown: the poke-storm rail — a full two-stage turn costs real
// seconds on a local engine; no model turn per footstep. Configurable
// (config `cooldown` / HARNESS_COOLDOWN); the dense default lives here.
func turnCooldown(cfg string) time.Duration {
	if d, err := time.ParseDuration(cfg); err == nil && d > 0 {
		return d
	}
	return 15 * time.Second
}

// AttachLedger wires the turn ledger (main.go; nil = clean skip).
func (h *Harness) AttachLedger(r GenerationRecorder) { h.recorder = r }

// loadTools reads the verb card (tools missing = single-stage mode).
func (h *Harness) loadTools() {
	if h.cfg.ToolsPath == "" {
		return
	}
	raw, err := os.ReadFile(h.cfg.ToolsPath)
	if err != nil {
		slog.Info("harness: no verb card, single-stage mode", "path", h.cfg.ToolsPath)
		return
	}
	var card struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &card); err != nil || len(card.Tools) == 0 {
		slog.Warn("harness: verb card unreadable, single-stage mode",
			"path", h.cfg.ToolsPath, "err", err)
		return
	}
	h.tools = card.Tools
	slog.Info("harness: verb card loaded", "verbs", len(h.tools))
}

// Run is the harness loop. It never returns an error unless something
// truly fatal happens — an unreachable LLM logs a warning and idles.
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
		"model", h.cfg.Model, "cadence", cadence, "verbs", len(h.tools))

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

	h.turn(ctx) // first beat: the boot survey (config sanity check)

	for {
		select {
		case <-ctx.Done():
			slog.Info("harness stopping", "err", ctx.Err())
			return nil
		case <-h.game.Poke():
			h.turn(ctx) // frontier: a player pushed into fresh country
		case <-ticker.C:
			h.turn(ctx) // the floor: cadence keeps the readout patched
		}
	}
}

// turn coalesces: one model turn in flight at a time (a poke arriving
// mid-turn folds into the next batch — the ring holds the events).
func (h *Harness) turn(ctx context.Context) {
	if !h.turnInFlight.CompareAndSwap(false, true) {
		return // a turn's already rolling with a fresher batch
	}
	defer h.turnInFlight.Store(false)
	if time.Since(h.lastTurn) < h.cooldown {
		return // quiet the door; stragglers fold into the next beat
	}
	h.cycle(ctx)
	h.lastTurn = time.Now()
}

// cycle: observe → stage 1 (persona) → stage 2 (interpreter) → rails →
// verbs. Errors log; the game never takes a beating for its optional
// game master.
func (h *Harness) cycle(ctx context.Context) {
	obs := h.game.Observe()
	if len(obs.Events) == 0 {
		// a quiet world doesn't earn a prompt
		slog.Info("harness: idle world, cycle skipped")
		return
	}
	slog.Info("harness: cycle begins", "events", len(obs.Events),
		"players", obs.World.PlayerCount, "towns", len(obs.Towns))

	// STAGE 1 — the persona turn (pure lore; no tool schema shown)
	prompt := h.prompt(obs)
	resp, err := h.output(ctx, prompt)
	if err != nil {
		slog.Warn("harness: prompt failed", "err", err)
		return
	}
	decision := resp.Text()
	slog.Info("harness: decision", "chars", len(decision))
	ledger := prompt + "\n---\n" + decision

	// STAGE 2 — the interpreter turn (native tool calls with the verb
	// card; the loop's single-stage mode skips it cleanly)
	var actions []GMAction
	if len(h.tools) > 0 {
		tr, terr := h.interpret(ctx, decision)
		if terr != nil {
			slog.Warn("harness: interpreter failed, single-stage fallback", "err", terr)
			actions = parseActions(decision)
		} else {
			actions = parseToolCalls(tr.ToolCalls())
			if len(actions) > 0 {
				ledger += "\n-> " + marshalActions(actions)
			}
		}
	} else {
		actions = parseActions(decision)
	}
	if len(actions) == 0 {
		slog.Info("harness: no actionable output this cycle (no-op)")
		h.record(ctx, ledger, "no-op")
		return
	}

	// the rails: cap per cycle, unknown verbs decline at the seam
	applied := 0
	for _, a := range actions[:min(len(actions), maxActions)] {
		if ctx.Err() != nil {
			return // stop applying the moment shutdown starts
		}
		if h.apply(ctx, a) {
			applied++
			ledger += "\n! " + a.Text
		}
	}
	h.record(ctx, ledger, fmt.Sprintf("applied=%d", applied))
}

// output is STAGE 1: the persona turn — NEVER shows the tools. In
// single-stage mode (no verb card) the text protocol suffix rides along.
func (h *Harness) output(ctx context.Context, user string) (*ChatResponse, error) {
	persona := strings.TrimSpace(h.cfg.SystemPrompt)
	if persona == "" {
		persona = cfgSystemFallback
	}
	if h.cfg.Model == "" {
		return nil, fmtErrNoFeature("harness model unset")
	}
	if len(h.tools) == 0 {
		persona += systemToolSuffix // legacy text protocol (mock CI keeps this)
	}
	req := ChatRequest{
		Model:       h.cfg.Model,
		Messages:    []ChatMessage{{Role: "system", Content: persona}, {Role: "user", Content: user}},
		Temperature: h.cfg.Temperature,
		MaxTokens:   max(700, h.cfg.MaxTokens),
	}
	return h.chat(ctx, req, "persona turn")
}

// interpret is STAGE 2: the decision's prose + the verb card → native
// tool calls. Same persona model when no separate interpreter backend
// is configured; the verb card rides the request either way.
func (h *Harness) interpret(ctx context.Context, decision string) (*ChatResponse, error) {
	model := h.cfg.Model
	if h.iclient != nil {
		model = h.cfg.InterpreterModel
	}
	if model == "" {
		return nil, fmtErrNoFeature("harness model unset (interpreter)")
	}
	req := ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "system", Content: interpreterPersona},
			{Role: "user", Content: decision},
		},
		Temperature: 0.2, // the interpreter is a converter, not a poet
		MaxTokens:   max(700, h.cfg.MaxTokens),
		Tools:       h.tools,
		ToolChoice:  "auto",
	}
	return h.chat(ctx, req, "interpreter turn")
}

// chat is the shared turn path with all the error checking in one
// place; the PERSONA stage rides h.client, the INTERPRETER stage rides
// h.iclient (its own provider — cactus, when configured).
func (h *Harness) chat(ctx context.Context, req ChatRequest, stage string) (*ChatResponse, error) {
	c := h.client
	if stage == "interpreter turn" && h.iclient != nil {
		c = h.iclient
	}
	resp, err := c.ChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", stage, err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s returned no choices", stage)
	}
	return resp, nil
}

// interpreterPersona: the no-persona converter seed — stage 2 carries
// zero lore; it maps the decision to the verb card and nothing else.
const interpreterPersona = `You convert a game master's decision into tool calls.
Use the provided tools. Call no tool if the decision needs none. Each
call's arguments match the tool schema exactly.`

// parseToolCalls flattens native tool calls into loop actions (the
// arguments field is a JSON-encoded param object string on the wire).
func parseToolCalls(calls []ToolCall) []GMAction {
	actions := make([]GMAction, 0, len(calls))
	for _, tc := range calls {
		a := GMAction{Type: tc.Function.Name}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &a); err != nil {
			slog.Warn("harness: tool args unreadable, verb dropped",
				"tool", tc.Function.Name, "err", err)
			continue
		}
		switch tc.Function.Name {
		case "announce", "spawn_enemies", "raise_village":
			actions = append(actions, a)
		default:
			// verbs decline at the seam (the interpreter never gets a
			// destructive card; unknown = old or hostile schema)
			slog.Warn("harness: verb not on the card, declined", "tool", tc.Function.Name)
		}
	}
	return actions
}

// apply runs one action through its rail; returns whether it landed.
func (h *Harness) apply(ctx context.Context, a GMAction) bool {
	var err error
	switch a.Type {
	case "announce":
		err = h.game.GMAnnounce(a.Text)
	case "spawn_enemies":
		err = h.game.GMSpawnEnemies(a.Count, a.Level)
	case "raise_village":
		err = h.game.GMRaiseVillage(a.Name)
	default:
		slog.Info("harness: action type not connected yet (phase 2)", "type", a.Type)
		return false
	}
	if err != nil {
		slog.Warn("harness: verb rejected", "type", a.Type, "err", err)
		return false
	}
	h.remember(a.Type + ": " + firstWord(a.Text, a.Name))
	return true
}

func firstWord(ss ...string) string {
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			return strings.Fields(t)[0]
		}
	}
	return "(unnamed)"
}

// remember folds short-term action memory (the no-repeat guard).
func (h *Harness) remember(line string) {
	h.lastActions = append(h.lastActions, line)
	if len(h.lastActions) > 4 {
		h.lastActions = h.lastActions[len(h.lastActions)-4:]
	}
}

// prompt builds the PERSONA turn: compact digest + the drained events.
func (h *Harness) prompt(obs game.GMReadout) string {
	var b strings.Builder
	b.WriteString("WORLD: ")
	b.WriteString(readoutLine(obs))
	b.WriteString("\nEVENTS SINCE LAST CYCLE:\n")
	for _, e := range obs.Events {
		fmt.Fprintf(&b, "- %s: %s\n", e.Kind, e.Text)
	}
	b.WriteString("What does the world murmur? Describe the next beat in-fiction —\n")
	b.WriteString("a mood, an omen, a rumor. Keep it short and concrete.\n\n")
	b.WriteString("You may also SHAPE the land at its frontier (the dark edge of\n")
	b.WriteString("the map), and this matters more than mere talk: found a\n")
	b.WriteString("settlement there, or send trouble onto its border. Empty\n")
	b.WriteString("frontier should usually EARN something — a building rising, or\n")
	b.WriteString("a band moving onto the edge. If you want that, say so plainly.\n")
	if len(obs.Towns) == 0 {
		b.WriteString("NO SETTLEMENT STANDS in the explored country yet — founding\n")
		b.WriteString("one would anchor this frontier for the wanderers.\n")
	} else if obs.Terrain.Settled < obs.Terrain.Frontier {
		b.WriteString("The explored country is thin — another outpost or a fresh\n")
		b.WriteString("band on the border would deepen the frontier.\n")
	}
	return b.String()
}

// readoutLine renders the world card in one compact line.
func readoutLine(obs game.GMReadout) string {
	var towns []string
	for _, t := range obs.Towns {
		towns = append(towns, fmt.Sprintf("%s(id=%d door=%d,%d)",
			t.Name, t.ID, t.Door[0], t.Door[1]))
	}
	return fmt.Sprintf("size %dx%d, players %d, enemies %d, towns [%s], frontier %d/%d",
		obs.World.W, obs.World.H, obs.World.PlayerCount, obs.World.EnemyCount,
		strings.Join(towns, " "), obs.Terrain.Frontier, obs.Terrain.Settled)
}

// record writes the turn's audit row (the generations ledger; nil
// recorder or a full store = skipped cleanly — the ledger aids, not rails).
func (h *Harness) record(ctx context.Context, turn, note string) {
	if h.recorder == nil {
		return
	}
	if _, err := h.recorder.RecordGeneration(ctx, storage.Generation{
		Kind:   "gm_cycle",
		Model:  h.cfg.Model,
		Prompt: turn,
		Output: note,
	}); err != nil {
		slog.Warn("harness: ledger row skipped", "err", err)
	}
}

func marshalActions(actions []GMAction) string {
	b, _ := json.Marshal(actions)
	return string(b)
}

// systemToolSuffix hangs the one-JSON-object protocol off the persona
// only in single-stage mode (when the verb card is absent).
const systemToolSuffix = `

You act by replying with EXACTLY ONE JSON object, and nothing else:
{"actions":[{"type":"announce","text":"..."}]}
Keep it small. If nothing needs doing, reply {"actions":[]}. Have ideas
about the world's mood, but respecting the rails: brief, concrete, in-fiction.`

// cfgSystemFallback keeps the loop promptable when the config has no
// persona at all (LoadConfig allows a voice-less harness).
const cfgSystemFallback = `You are the game master of a multiplayer dungeon world.`

func fmtErrNoFeature(msg string) error { return errors.New(msg) }

// maxActions caps the per-cycle actions (the rails; a well-mannered
// model doesn't get to cascade ten tools at once).
const maxActions = 3
