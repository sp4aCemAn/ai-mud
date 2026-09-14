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
	talk  *game.Talk  // open conversation (nil = none; slice 3)
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

	// camera: the viewport's top-left world tile. Moves only when the
	// player walks out of the padded dead zone (never hard-centered —
	// stops chars repaint churn on every step).
	camX, camY int

	// cache key extras: the window itself is part of the identity —
	// a pan or a pane change re-cuts the slice, a plain move doesn't.
	cacheCamX, cacheCamY, cacheW, cacheH int
}

// camPad is the dead-zone padding: how many tiles of runway the player
// keeps from the viewport edge before the camera pans.
const camPad = 3

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
		g.panCamera()
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
			g.panCamera()
			g.refreshTerrainCache()
		}
		return g, stateTick()
	case resizeDoneMsg:
		// trailing edge of the resize streak: one regen per burst.
		// The world only GROWS to fit a bigger pane — a smaller pane
		// keeps the world and gets a panning viewport instead
		g.resizeScheduled = false
		if g.pc == nil {
			return g, nil
		}
		if c := g.cv(); c != nil {
			field := innerSize(g.width, g.height)
			nw, nh := field.w, field.h
			if g.world.W > nw {
				nw = g.world.W
			}
			if g.world.H > nh {
				nh = g.world.H
			}
			g.apply(c.Resize(g.fp, nw, nh))
			g.panCamera()
			g.refreshTerrainCache()
		}
		return g, nil
	case tea.WindowSizeMsg:
		g.width = msg.Width
		g.height = msg.Height
		// the pane may have changed even when the world doesn't —
		// a pan or slice re-cut happens with the next refresh
		g.panCamera()
		g.refreshTerrainCache()
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

	case g.talk != nil:
		// conversations are modal: enter advances, esc walks away
		if c != nil {
			switch msg.String() {
			case "enter":
				g.applyTalk(c.Command(g.fp, "talk-advance", 0))
			case "esc":
				g.applyTalk(c.Command(g.fp, "talk-close", 0))
			}
		}
		return g, nil

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
	g.panCamera()
	g.refreshTerrainCache()
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
	if r.Talk != nil {
		g.talk = r.Talk
	}
	g.log = append(g.log, r.Events...)
	if len(g.log) > 3 {
		g.log = g.log[len(g.log)-3:]
	}
	g.panCamera()
	g.refreshTerrainCache()
}

// applyTalk mirrors the talk path (enter/esc keys): a nil Talk in the
// reply means the server-side session closed → the overlay clears.
func (g *GameScreen) applyTalk(r game.Result) {
	g.apply(r)
	if r.Talk == nil {
		g.talk = nil
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
	case g.talk != nil:
		hintTxt = "[enter] next line · esc walk away"
	case g.inv:
		hintTxt = "u/m use · esc close"
	}

	// Diablo layout: full-bleed field, HUD strip pinned bottom-center,
	// event log + hints under it (or the nudge banner on top).
	// LEFT-join: every child change (a log line's width, a wide 町
	// cell) would otherwise re-center the field block and slide the
	// whole map — centering only inside fixed-width children.
	body := lipgloss.JoinVertical(lipgloss.Left,
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
	case g.talk != nil:
		content = Overlay(content, renderTalk(g.talk))
	case g.inv:
		content = Overlay(content, renderPack(g.p))
	}
	return content
}

// renderTalk is the conversation overlay: the NPC's line, the quest
// marker when the line carries one, and the modal hints.
func renderTalk(t *game.Talk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", t.Name)
	if t.Line < len(t.Lines) {
		b.WriteString(hint(t.Lines[t.Line].Text) + "\n")
		if t.Lines[t.Line].Quest != nil {
			b.WriteString("the giver's eyes harden — a task follows\n")
		}
	}
	if len(t.Lines) <= 1 {
		b.WriteString(" [enter] finish · esc walk away")
	} else {
		fmt.Fprintf(&b, " [enter] next (%d/%d) · esc walk away", t.Line+1, len(t.Lines))
	}
	return Panel{Title: "「" + t.TownName + "」", Content: b.String()}.Render()
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

// renderField draws the visible slice of the world (terrain + dots,
// camera-windowed); @ is the player. The tile block is cached by
// (version, camera window); the @ composites on top per frame —
// positions never hit the cache.
func (g GameScreen) renderField() string {
	if g.terrainCache == "" {
		vw, vh := g.fieldSize()
		return g.buildField(vw, vh) // no cache in this copy — miss is cheap
	}
	// composite @ over the cached block (cheap line surgery), at the
	// CAMERA-relative position — and cell-aware for wide glyphs
	lines := strings.Split(g.terrainCache, "\n")
	rx, ry := g.p.X-g.camX, g.p.Y-g.camY
	if ry >= 0 && ry < len(lines) {
		row := []rune(lines[ry])
		if rx < len(row) && g.worldGlyph(g.p.X, g.p.Y) != '@' && rx >= 0 {
			cell := cellsBefore(row, rx)
			// '@' paints one cell; over a wide glyph it must claim
			// both (the original's width) or the whole row shifts
			filler := ""
			if glyphCellWidth(row[rx]) > 1 {
				filler = " "
			}
			rewritten := append(row[:cell:cell], '@')
			rewritten = append(rewritten, []rune(filler+string(row[rx+1:]))...)
			lines[ry] = string(rewritten)
		}
	}
	return strings.Join(lines, "\n")
}

// fieldSize is the field pane's size in glyphs: what's left of the
// terminal after the HUD strip, clamped to the world's own dims.
// When the pane is at least as large as the world the viewport equals
// the world (today's full-block behavior; the camera stays at 0,0).
func (g GameScreen) fieldSize() (vw, vh int) {
	s := innerSize(g.width, g.height)
	vw, vh = s.w, s.h
	if g.world.W > 0 && vw > g.world.W {
		vw = g.world.W
	}
	if g.world.H > 0 && vh > g.world.H {
		vh = g.world.H
	}
	return
}

// panCamera keeps the player's dot inside the viewport's padded dead
// zone, moving the window as little as possible (no hard centering).
// The camera lives in ABSOLUTE coords (the world may serve a rect that
// doesn't start at 0,0 — infinite plane growth shifts it). Worlds
// smaller than the pane pin the camera to the world's origin. Called
// from the Update paths only (View must not mutate state).
func (g *GameScreen) panCamera() {
	vw, vh := g.fieldSize()
	axis := func(pos, size, cam, origin, pane int) int {
		if pane >= size {
			return origin // whole world visible
		}
		hi := origin + size - pane
		if pos < cam+camPad {
			cam = pos - camPad
		}
		if pos > cam+pane-1-camPad {
			cam = pos - (pane - 1 - camPad)
		}
		return clamp(cam, origin, hi)
	}
	g.camX = axis(g.p.X, g.world.W, g.camX, g.world.OriginX, vw)
	g.camY = axis(g.p.Y, g.world.H, g.camY, g.world.OriginY, vh)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// refreshTerrainCache rebuilds the cached visible slice when the world
// version moved OR the camera/pane moved (the cache key is the window,
// not just the version). Called ONLY from Update paths (never View —
// bubbletea discards mutations made there).
func (g *GameScreen) refreshTerrainCache() {
	vw, vh := g.fieldSize()
	if g.terrainCache == "" || g.terrainVersion != g.world.Version ||
		g.cacheCamX != g.camX || g.cacheCamY != g.camY ||
		g.cacheW != vw || g.cacheH != vh {
		g.terrainCache = g.buildField(vw, vh)
		g.terrainVersion = g.world.Version
		g.cacheCamX, g.cacheCamY, g.cacheW, g.cacheH = g.camX, g.camY, vw, vh
	}
}

// buildField builds the version-cached VISIBLE world block (the
// [camX,camX+vw) × [camY,camY+vh) slice of terrain with dots on top —
// no @; the player composites per frame. Row display widths are
// NORMALIZED (wide glyphs like 町 paint two cells): every row is
// padded to the same cell width, or lipgloss's per-row centering
// would jitter the whole map by a cell whenever a wide glyph scrolls.
func (g GameScreen) buildField(vw, vh int) string {
	dots := map[[2]int]rune{}
	for _, d := range g.world.Dots {
		glyph := 'x' // enemy dot
		switch d.Kind {
		case "npc":
			glyph = '$'
		case "npc_town":
			glyph = '☺' // a town NPC's dot (slice 3 shades by role)
		case "player_town":
			glyph = '+' // a co-present player's dot in your town view
		}
		dots[[2]int{d.X, d.Y}] = glyph
	}

	// first pass: rows as cell-normalized strings (trailing pad)
	rows := make([]string, vh)
	maxDisp := 0
	disp := make([]int, vh)
	for y := g.camY; y < g.camY+vh; y++ {
		var b strings.Builder
		w := 0
		for x := g.camX; x < g.camX+vw; x++ {
			if glyph, ok := dots[[2]int{x, y}]; ok {
				b.WriteRune(glyph)
				w += glyphCellWidth(glyph)
				continue
			}
			b.WriteRune(g.worldGlyph(x, y))
			w += glyphCellWidth(g.worldGlyph(x, y))
		}
		rows[y-g.camY] = b.String()
		if w > maxDisp {
			maxDisp = w
		}
		disp[y-g.camY] = w
	}
	// second pass: pad every row to the pane's cell width
	for i := range rows {
		if pad := maxDisp - disp[i]; pad > 0 {
			rows[i] += strings.Repeat(" ", pad)
		}
	}
	return strings.Join(rows, "\n")
}

// worldGlyph is terrain with dots on top at ABSOLUTE coords; the
// world snapshot covers [OriginX..OriginX+W) × [OriginY..OriginY+H).
// The world's canonical gate glyph stays 町 (data + tools read it),
// but the RENDER maps it to a 1-cell ASCII block: wide-cell paint is
// AMBIGUOUS across SSH clients (CJK mode), and every ambiguous cell
// was sliding the row's centered composition — horizontal jitter.
func (g GameScreen) worldGlyph(x, y int) rune {
	r := g.worldGlyphData(x, y)
	if r == game.TileGate {
		return '#' // 1-cell render of the wide kanji (bail-out clause)
	}
	return r
}

// worldGlyphData is terrain + dots, absolute → rect-local, no render
// remapping (the raw snapshot glyph).
func (g GameScreen) worldGlyphData(x, y int) rune {
	ly, lx := y-g.world.OriginY, x-g.world.OriginX
	if g.world.Tiles == nil || ly < 0 || ly >= len(g.world.Tiles) {
		return '·'
	}
	b := []rune(g.world.Tiles[ly])
	if lx < 0 || lx >= len(b) {
		return '·'
	}
	return b[lx]
}

// glyphCellWidth is the display-cell width of one world glyph. CJK
// blocks (the gate 町) paint two terminal cells; everything else is
// one. The field rows stay rune-indexed, but splices and column-claims
// translate through this (row display width can exceed its rune count).
func glyphCellWidth(r rune) int {
	return 1 // wide-glyph paint is ambiguous across SSH clients; render 1-cell
}

// cellsBefore counts the display cells used by the first x tiles of a
// rune row — the '@' splice lands on a CELL, not a rune slot.
func cellsBefore(row []rune, x int) int {
	c := 0
	for i := 0; i < x && i < len(row); i++ {
		c += glyphCellWidth(row[i])
	}
	return c
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
