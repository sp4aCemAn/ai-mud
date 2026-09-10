package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/game"
)

// stateTickEvery is how often the screen syncs authoritative state from
// the world. It keeps the server's lastSeen fresh (so idle players are
// not reaped), and pulls server-driven changes (damage, regeneration,
// respawns) into the panels.
var stateTickEvery = 2 * time.Second

type stateTickMsg struct{}

func stateTick() tea.Cmd {
	return tea.Tick(stateTickEvery, func(time.Time) tea.Msg { return stateTickMsg{} })
}

// verify steps inside the overlay: reveal/copy the one-time password,
// or replace it; both ack → verified.
const (
	vactView  = iota // nothing pending: show nudge actions
	vactInput        // typing (name or password)
	vactDone
)

// GameScreen is the playable view: the player's dot on the terrain
// grid, stats, enemy dots and the merchant, plus floating windows
// (pack, fight, store) built from the kit. Unverified accounts get a
// persistent banner + a quit guard until the one-time password is
// acknowledged.
type GameScreen struct {
	pc    game.PlayerView // PlayerView + (optionally) CombatView
	id    auth.Identity
	accts *auth.Accounts

	fp    string
	p     game.Player
	world game.World
	fight *game.Fight // live duel (nil = none)
	shop  *game.Shop  // store overlay (nil = closed)
	inv   bool        // pack window open
	log   []string    // latest world event lines

	// verify nudge state
	unverified bool // banner + quit guard active
	verify     bool // overlay open
	vInput     textinput.Model
	vMode      string // "" | "name" | "pass"
	vErr       string
	copied     bool // "I've copied it" ack set down

	quitAsk bool // double-ctrl+c quit guard (unverified)

	width  int
	height int

	// render cache: the terrain block rebuilds only when the world
	// version changes, not on every frame (hp bars, dots).
	terrainCache    string
	terrainVersion  uint64
	pendingResizeW  int
	pendingResizeH  int
	resizeScheduled bool
}

// resizeDebounce coalesces streaks of WindowSizeMsgs into one world
// regen (trailing edge).
const resizeDebounce = 150 * time.Millisecond

type resizeDoneMsg struct{}

func scheduleResize() tea.Cmd {
	return tea.Tick(resizeDebounce, func(time.Time) tea.Msg { return resizeDoneMsg{} })
}

func newGameScreen(id auth.Identity, pc game.PlayerView) GameScreen {
	g := GameScreen{
		id:    id,
		fp:    id.Fingerprint,
		pc:    pc,
		accts: auth.NewAccounts(nil),
		// key-backed sessions are verified by the handshake itself;
		// only password-only accounts (anonymous creations that
		// somehow skipped the ack) carry the nudge + quit guard
		unverified: id.Verified == false && id.User.Name != "guest",
	}
	g.vInput = textinput.New()
	g.vInput.CharLimit = 32
	g.vInput.EchoMode = textinput.EchoPassword

	if pc != nil {
		g.p = pc.Join(id.Fingerprint, id.User.Name) // idempotent
		if c, ok := pc.(game.CombatView); ok {
			g.world = c.WorldView(id.Fingerprint)
		}
		g.refreshTerrainCache()
	}
	return g
}

// cv returns the richer world-action interface when the PlayerView
// supplies combat (plain fakes in tests may not).
func (g GameScreen) cv() game.CombatView {
	c, ok := g.pc.(game.CombatView)
	if ok {
		return c
	}
	return nil
}

func (g GameScreen) Init() tea.Cmd { return stateTick() }

func (g GameScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stateTickMsg:
		// keys land a fresh Result themselves; the pull is near-free,
		// so keep it simple and always refresh
		if g.pc != nil {
			if p, ok := g.pc.State(g.fp); ok {
				g.p = p
			}
			if c := g.cv(); c != nil {
				g.world = c.WorldView(g.fp)
			}
			g.refreshTerrainCache()
		}
		return g, stateTick()
	case resizeDoneMsg:
		// trailing edge of the resize streak: one regen per burst
		g.resizeScheduled = false
		if g.pc == nil {
			return g, nil
		}
		if c := g.cv(); c != nil {
			field := innerSize(g.width, g.height)
			g.apply(c.Resize(g.fp, field.w, field.h))
			g.refreshTerrainCache()
		}
		return g, nil
	case tea.WindowSizeMsg:
		g.width = msg.Width
		g.height = msg.Height
		// stash dims; the regen fires once the burst settles
		g.pendingResizeW, g.pendingResizeH = msg.Width, msg.Height
		if g.resizeScheduled {
			return g, nil
		}
		g.resizeScheduled = true
		return g, scheduleResize()
	case tea.KeyMsg:
		return g.keyInput(msg)
	}
	return g, nil
}

// keyInput handles keys in priority order: quit guard, verify overlay,
// store, duel, pack, plain movement.
func (g GameScreen) keyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := g.cv()

	// quit guard: first ctrl+c warns, a second quits
	if msg.String() == "ctrl+c" {
		if g.quitAsk || !g.unverified {
			return g, tea.Quit
		}
		g.quitAsk = true
		return g, nil
	}
	g.quitAsk = false

	switch {
	case g.verify:
		return g.verifyInput(msg)

	case g.shop != nil:
		if c != nil {
			switch msg.String() {
			case "1", "2", "3", "4":
				g.apply(c.Command(g.fp, "buy", int(msg.String()[0]-'0')))
			case "esc", "enter":
				g.apply(c.Command(g.fp, "close", 0))
			case "i":
				g.shop = nil
				g.inv = true
			}
		} else if msg.String() == "esc" || msg.String() == "i" {
			g.shop = nil
		}
		return g, nil

	case g.fight != nil:
		if c != nil {
			switch msg.String() {
			case "a":
				g.apply(c.Command(g.fp, "attack", 0))
			case "c":
				g.apply(c.Command(g.fp, "cast", 0))
			case "f", "esc":
				g.apply(c.Command(g.fp, "flee", 0))
			case "i":
				g.inv = true
			}
		} else if msg.String() == "esc" {
			g.fight = nil
		}
		return g, nil

	case g.inv:
		switch msg.String() {
		case "esc", "i":
			g.inv = false
		case "u": // drink a dark potion
			if c != nil {
				g.apply(c.Command(g.fp, "use", 1))
			}
		case "m": // sip a mana draught
			if c != nil {
				g.apply(c.Command(g.fp, "use", 2))
			}
		}
		return g, nil
	}

	// free play
	dx, dy := 0, 0
	switch msg.String() {
	case "v":
		if g.unverified {
			g.verify = true
			g.vErr = ""
			g.vMode = ""
			g.vInput.Blur()
		}
	case "i":
		g.inv = true
	case "up", "w", "k":
		dy = -1
	case "down", "s", "j":
		dy = 1
	case "left", "a", "h":
		dx = -1
	case "right", "d", "l":
		dx = 1
	}
	if dx == 0 && dy == 0 {
		return g, nil
	}
	if c != nil {
		g.apply(c.Interact(g.fp, dx, dy))
	} else if p, ok := g.pc.Move(g.fp, dx, dy); ok {
		g.p = p
	}
	return g, nil
}

// verifyInput drives the verify-now overlay: copy/set password, pick
// your own name.
func (g GameScreen) verifyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		g.verify = false
		g.vMode = ""
		g.vErr = ""
		g.vInput.Blur()
		return g, nil
	case "enter":
		if g.vMode == "" {
			return g, nil
		}
		val := strings.TrimSpace(g.vInput.Value())
		if val == "" {
			g.vErr = "empty — try again"
			return g, nil
		}
		if g.vMode == "name" {
			if !ValidName(val) {
				g.vErr = "names are 3–16 chars, letters first"
				return g, nil
			}
			if err := g.accts.RenameAccount(g.id.User.Name, val); err != nil {
				g.vErr = "name taken — choose another"
				return g, nil
			}
			g.vMode = ""
			g.vInput.Blur()
			return g.applyIdentity(val)
		}
		// password mode: setting your own is the ack
		if err := g.accts.SetPassword(g.id.User.Name, val); err != nil {
			g.vErr = "the dark refused it — try again"
			return g, nil
		}
		return g.applyIdentity(g.id.User.Name)
	case "c":
		// mark the generated password as acknowledged/kept
		if err := g.accts.AckPassword(g.id.User.Name); err != nil {
			g.vErr = "failed to ack — retry"
			return g, nil
		}
		return g.applyIdentity(g.id.User.Name)
	case "n":
		g.vMode = "name"
		g.vInput.EchoMode = textinput.EchoNormal
		g.vInput.Placeholder = "new name"
		g.vInput.Focus()
		g.vErr = ""
		return g, textinput.Blink
	case "p":
		g.vMode = "pass"
		g.vInput.EchoMode = textinput.EchoPassword
		g.vInput.EchoCharacter = '•'
		g.vInput.Placeholder = "your own password"
		g.vInput.Focus()
		g.vErr = ""
		return g, textinput.Blink
	}

	if g.vMode != "" {
		var cmd tea.Cmd
		g.vInput, cmd = g.vInput.Update(msg)
		return g, cmd
	}
	return g, nil
}

// applyIdentity refetches the account state and swaps the identity —
// the router rebuilds this screen with fresh flags on completion.
func (g GameScreen) applyIdentity(name string) (tea.Model, tea.Cmd) {
	acc, err := g.accts.Account(name)
	if err != nil {
		g.vErr = "storage hiccup — verified locally"
		// optimistic: mark session-verified, keep playing
		g.unverified = false
		g.verify = false
		return g, nil
	}
	id := g.accts.IdentityForAccount(acc)
	id.Fingerprint = g.fp
	return g, swapIdentity(id, ScreenGame)
}

// apply eats a world Result: player, world, overlays, log lines.
// Pointer receiver: as a value receiver the writes landed in a copy
// and were thrown away — every keystroke's Result was discarded until
// the next stateTick re-pulled the server state (the 2s-render bug).
func (g *GameScreen) apply(r game.Result) {
	g.p = r.Player
	g.world = r.World
	g.fight = r.Fight
	g.shop = r.Shop
	g.log = append(g.log, r.Events...)
	if len(g.log) > 3 {
		g.log = g.log[len(g.log)-3:]
	}
}

func (g GameScreen) View() string {
	if g.width == 0 {
		return "entering the world…"
	}

	hud := renderHUD(g.p)
	hintTxt := "hjkl/arrows move · bump stores/fights to engage · i pack · ctrl+c quit"
	switch {
	case g.fight != nil:
		hintTxt = "[a]ttack · [c]ast · [f]lee"
	case g.shop != nil:
		hintTxt = "1-4 buy · esc leave"
	case g.inv:
		hintTxt = "u/m use · esc close"
	}

	// Diablo layout: full-bleed field, HUD strip pinned bottom-center,
	// event log + hints under it (or the nudge banner on top).
	body := lipgloss.JoinVertical(lipgloss.Center,
		g.renderField(),
		hint(g.renderLog()),
		lipgloss.NewStyle().Width(g.width).Align(lipgloss.Center).Render(hud),
		hint(hintTxt),
	)
	content := body
	if g.unverified {
		content = lipgloss.JoinVertical(lipgloss.Left,
			hint("⚠ unverified: your body would die here — set/copy the password. [v]erify"),
			content,
		)
	}
	if g.width > 0 {
		content = lipgloss.Place(g.width, g.height,
			lipgloss.Center, lipgloss.Bottom, content)
	}

	switch {
	case g.verify:
		content = Overlay(content, g.renderVerify())
	case g.shop != nil:
		content = Overlay(content, renderShop(g.shop))
	case g.fight != nil:
		content = Overlay(content, renderFight(g.fight))
	case g.inv:
		content = Overlay(content, renderPack(g.p))
	}
	return content
}

// innerSize converts a terminal size into a world size: what's left
// after the HUD strip (bars + log + hint lines) and glyph gutters.
func innerSize(termW, termH int) (sz struct{ w, h int }) {
	w := termW - 2 // left/right gutters so glyphs never soft-wrap
	h := termH - 5 // HUD + log line + hint line + top gutter
	if w < game.MinW {
		w = game.MinW
	}
	if h < game.MinH {
		h = game.MinH
	}
	if w > game.MaxW {
		w = game.MaxW
	}
	if h > game.MaxH {
		h = game.MaxH
	}
	sz.w, sz.h = w, h
	return sz
}

// renderHUD is the Diablo-style bottom strip: bars + vitals, plus a
// small dim pos readout (smoke tests assert on it).
func renderHUD(p game.Player) string {
	hp := Bar("hp", p.HP, p.MaxHP, 10)
	mana := Bar("mp", p.Mana, p.MaxMana, 10)
	vitals := fmt.Sprintf("⚔%d ⛨%d ❦%d", p.Atk, p.Def, p.Coins)
	return fmt.Sprintf("%s   %s   %s   %d,%d", hp, mana, vitals, p.X, p.Y)
}

// renderVerify is the verify-now overlay: the one-time password,
// opt-in rename, or set-your-own password.
func (g GameScreen) renderVerify() string {
	var b strings.Builder
	b.WriteString("your body plays under an account born of whim:\n\n")
	fmt.Fprintf(&b, "  callsign:  %s\n", g.id.User.Name)
	b.WriteString("\nyour account can be verified in three ways (any of them):\n\n")
	b.WriteString("  [c] 'I've copied/kept the one-time password'  ← easiest\n")
	b.WriteString("  [p] set your own password\n")
	b.WriteString("  [n] rename yourself afterwards too\n\n")
	if g.vMode != "" {
		b.WriteString(g.vInput.View() + "\n")
	}
	if g.vErr != "" {
		fmt.Fprintf(&b, "\n✗ %s\n", g.vErr)
	}
	b.WriteString("\nesc keep playing unverified")
	return Panel{Title: "Verify Now", Content: b.String()}.Render()
}

// renderField draws the terrain + dots; @ is the player, x an enemy
// dot, $ the merchant — plain text so Overlay stays ANSI-correct.
// The tile block is cached by world version (the player's @ composites
// on top per frame — positions never hit the cache).
func (g GameScreen) renderField() string {
	if g.terrainCache == "" || g.terrainVersion != g.world.Version {
		return g.buildField() // no cache in this copy — miss is cheap
	}
	// composite @ over the cached block (cheap line surgery)
	lines := strings.Split(g.terrainCache, "\n")
	if g.p.Y < len(lines) {
		row := []rune(lines[g.p.Y])
		if g.p.X < len(row) && g.worldGlyph(g.p.X, g.p.Y) != '@' {
			row[g.p.X] = '@'
			lines[g.p.Y] = string(row)
		}
	}
	return strings.Join(lines, "\n")
}

// refreshTerrainCache rebuilds the cached block when the world
// version moved. Called ONLY from Update paths (never View — bubbletea
// discards mutations made there).
func (g *GameScreen) refreshTerrainCache() {
	if g.terrainCache == "" || g.terrainVersion != g.world.Version {
		g.terrainCache = g.buildField()
		g.terrainVersion = g.world.Version
	}
}

// buildField builds the version-cached world block (no @).
func (g GameScreen) buildField() string {
	h := g.world.H
	ww := g.world.W
	if h == 0 {
		h = len(g.world.Tiles)
	}
	if ww == 0 {
		if len(g.world.Tiles) > 0 {
			ww = len([]rune(g.world.Tiles[0]))
		} else {
			ww = game.WorldW
		}
	}
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < ww; x++ {
			b.WriteRune(g.worldGlyph(x, y))
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// worldGlyph is terrain with dots on top.
func (g GameScreen) worldGlyph(x, y int) rune {
	if g.world.Tiles == nil || y >= len(g.world.Tiles) {
		return '·'
	}
	row := []rune(g.world.Tiles[y])
	if x < len(row) {
		return row[x]
	}
	return '·'
}

// renderLog keeps the last few lines of world events visible.
func (g GameScreen) renderLog() string {
	if len(g.log) == 0 {
		return ""
	}
	lines := make([]string, 0, len(g.log))
	for _, l := range g.log {
		lines = append(lines, hint(l))
	}
	return strings.Join(lines, "\n")
}

// renderFight is the combat overlay.
func renderFight(f *game.Fight) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (x%d) — round %d\n\n", f.Name, f.Count, f.Round)
	b.WriteString(Bar("they", f.EnemyHP, f.EnemyMaxHP, 10) + "\n")
	b.WriteString(Bar("you ", f.PlayerHP, f.PlayerMaxHP, 10) + "\n")
	if f.Last != "" {
		fmt.Fprintf(&b, "\n%s", f.Last)
	}
	b.WriteString("\n\n[a]ttack · [c]ast (3 mana) · [f]lee")
	return Panel{Title: "unholy duel", Content: b.String()}.Render()
}

// renderShop is the store overlay.
func renderShop(s *game.Shop) string {
	var b strings.Builder
	b.WriteString(s.Pitch + "\n\n")
	for i, w := range s.Lines {
		fmt.Fprintf(&b, "  [%d] %-18s %-22s %dc\n", i+1, w.Name, w.Desc, w.Price)
	}
	b.WriteString("\nesc close")
	return Panel{Title: s.Keeper, Content: b.String()}.Render()
}

// renderPack is the pack overlay.
func renderPack(p game.Player) string {
	var b strings.Builder
	if len(p.Inventory) == 0 {
		b.WriteString("— nothing yet —\n")
	} else {
		for name, n := range p.Inventory {
			fmt.Fprintf(&b, "%-16s x%d\n", name, n)
		}
	}
	fmt.Fprintf(&b, "\nu use dark potion · m sip draught · esc close")
	return Panel{Title: "Pack", Content: b.String()}.Render()
}

// shortFP truncates a fingerprint for display.
func shortFP(fp string) string {
	if len(fp) > 12 {
		return "…" + fp[len(fp)-8:]
	}
	if fp == "" {
		return "anonymous"
	}
	return fp
}
