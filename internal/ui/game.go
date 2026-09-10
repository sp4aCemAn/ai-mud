package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// GameScreen is the playable view: the player's dot on the terrain
// grid, stats, enemy dots and the merchant, plus floating windows
// (pack, fight, store) built from the kit.
type GameScreen struct {
	pc game.PlayerView // PlayerView + (optionally) CombatView

	fp   string
	p    game.Player
	world game.World
	fight *game.Fight // live duel (nil = none)
	shop  *game.Shop  // store overlay (nil = closed)
	inv   bool        // pack window open
	log   []string    // latest world events

	width  int
	height int
}

func newGameScreen(pc game.PlayerView, fp, name string) GameScreen {
	g := GameScreen{pc: pc, fp: fp}
	if pc != nil {
		g.p = pc.Join(fp, name) // idempotent: reconnect reuses the body
		g.world = g.worldNow()
	}
	return g
}

// cv returns the GameScreen's world-action surface, nil if the view
// behind it is the plain PlayerView (tests).
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
		if g.pc != nil {
			if p, ok := g.pc.State(g.fp); ok {
				g.p = p
			}
			if c := g.cv(); c != nil {
				g.world = c.WorldView(g.fp)
			}
		}
		return g, stateTick()
	case tea.WindowSizeMsg:
		g.width = msg.Width
		g.height = msg.Height
	case tea.KeyMsg:
		switch {
		case g.shop != nil:
			// the store has focus
			switch msg.String() {
			case "1", "2", "3", "4":
				g.apply(c.Command(g.fp, "buy", int(msg.String()[0]-'0')))
			case "esc", "enter":
				g.apply(c.Command(g.fp, "close", 0))
			case "i":
				g.shop = nil
				g.inv = true
			}
			return g, nil

		case g.fight != nil:
			// the duel has focus
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
			return g, nil

		case g.inv:
			// pack has focus: movement must not leak through
			switch msg.String() {
			case "esc", "i":
				g.inv = false
			case "u": // drink a dark potion
				g.apply(c.Command(g.fp, "use", 1))
			case "m": // sip a mana draught
				g.apply(c.Command(g.fp, "use", 2))
			}
			return g, nil
		}

		if c := g.cv(); c != nil {
			dx, dy := 0, 0
			switch msg.String() {
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
			if dx != 0 || dy != 0 {
				g.apply(c.Interact(g.fp, dx, dy))
			}
		} else {
			dx, dy := 0, 0
			switch msg.String() {
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
			if dx != 0 || dy != 0 {
				if p, ok := g.pc.Move(g.fp, dx, dy); ok {
					g.p = p
				}
			}
		}
	}
	return g, nil
}

// apply eats a Result: player, world, overlays and log line all land
// at once.
func (g *GameScreen) apply(r game.Result, _ ...tea.Cmd) {
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

	layout := lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "the world", Content: g.renderField()}.Render(),
		renderStats(g.p),
	)

	hintTxt := "hjkl/arrows move · enter stores by bumping · i pack"
	if g.fight != nil {
		hintTxt = "[a]ttack · [c]ast · [f]lee"
	} else if g.shop != nil {
		hintTxt = "1-4 buy · esc leave"
	} else if g.inv {
		hintTxt = "u/m use item · esc/i close"
	}

	content := frame(g.width, g.height,
		lipgloss.JoinVertical(lipgloss.Left,
			layout,
			g.renderLog(),
			hint(hintTxt+" · ctrl+c quit"),
		),
	)

	switch {
	case g.shop != nil:
		content = Overlay(content, renderShop(g.shop))
	case g.fight != nil:
		content = Overlay(content, renderFight(g.fight))
	case g.inv:
		content = Overlay(content, renderPack(g.p))
	}
	return content
}

// renderField draws the terrain + dots; @ is the player, x an enemy
// dot, $ the merchant — plain text for Overlay compositing.
func (g GameScreen) renderField() string {
	var b strings.Builder
	for y := 0; y < game.WorldH; y++ {
		for x := 0; x < game.WorldW; x++ {
			if x == g.p.X && y == g.p.Y {
				b.WriteString("@")
				continue
			}
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

// renderStats is the side panel: identity + HP/Mana/coin/rage stats.
func renderStats(p game.Player) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name  %s\n", p.Name)
	fmt.Fprintf(&b, "lv    %d  xp %d\n", p.Level, p.XP)
	b.WriteString(Bar("hp  ", p.HP, p.MaxHP, 10) + "\n")
	b.WriteString(Bar("mana", p.Mana, p.MaxMana, 10) + "\n")
	fmt.Fprintf(&b, "coin  %d\n", p.Coins)
	fmt.Fprintf(&b, "atk   %d   def %d\n", p.Atk, p.Def)
	fmt.Fprintf(&b, "pos   %d,%d", p.X, p.Y)
	return Panel{Title: "stats", Content: b.String()}.Render()
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

// renderPack is the inventory overlay (windowed over the field).
func renderPack(p game.Player) string {
	var b strings.Builder
	if len(p.Inventory) == 0 {
		b.WriteString("— nothing yet —")
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
