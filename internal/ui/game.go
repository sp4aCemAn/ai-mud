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
// not reaped) and is the hook where server-driven changes (damage,
// regen, world events) will reach the UI.
var stateTickEvery = 2 * time.Second

type stateTickMsg struct{}

func stateTick() tea.Cmd {
	return tea.Tick(stateTickEvery, func(time.Time) tea.Msg { return stateTickMsg{} })
}

// GameScreen is the playable view: the player's dot on the world grid,
// a stats panel, and floating windows (inventory now; trade, dialogs,
// etc. later) built from the same kit pieces.
type GameScreen struct {
	pc game.PlayerView
	fp string

	p   game.Player // cached copy of authoritative state
	inv bool        // inventory window open

	width  int
	height int
}

func newGameScreen(pc game.PlayerView, fp, name string) GameScreen {
	g := GameScreen{pc: pc, fp: fp}
	if pc != nil {
		g.p = pc.Join(fp, name) // idempotent: reconnect reuses the body
	}
	return g
}

func (g GameScreen) Init() tea.Cmd { return stateTick() }

func (g GameScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stateTickMsg:
		if g.pc != nil {
			if p, ok := g.pc.State(g.fp); ok {
				g.p = p
			}
		}
		return g, stateTick()
	case tea.WindowSizeMsg:
		g.width = msg.Width
		g.height = msg.Height
	case tea.KeyMsg:
		if g.inv {
			// overlay has focus: movement must not leak through
			switch msg.String() {
			case "esc", "i":
				g.inv = false
			}
			return g, nil
		}

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
	return g, nil
}

func (g GameScreen) View() string {
	if g.width == 0 {
		return "entering the world…"
	}

	layout := lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "the world", Content: renderField(g.p)}.Render(),
		renderStats(g.p),
	)

	content := frame(g.width, g.height,
		lipgloss.JoinVertical(lipgloss.Left,
			layout,
			hint("hjkl/arrows move · i inventory · ctrl+c quit"),
		),
	)

	if g.inv {
		content = Overlay(content,
			Panel{
				Title:   "Inventory",
				Content: "— nothing yet —\n\nthe pack is a future phase\n\nesc / i close",
			}.Render())
	}
	return content
}

// renderField draws the world grid: dots everywhere, the player as @.
// Plain text only — this is the background the Overlay composites onto.
func renderField(p game.Player) string {
	rows := make([]string, game.WorldH)
	for y := 0; y < game.WorldH; y++ {
		var b strings.Builder
		for x := 0; x < game.WorldW; x++ {
			if x == p.X && y == p.Y {
				b.WriteString("@")
			} else {
				b.WriteString("·")
			}
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

// renderStats is the side panel: identity + the HP/Mana bars (kit.Bar)
// so future screens (party frames, enemy info) reuse the same bars.
func renderStats(p game.Player) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name  %s\n", p.Name)
	fmt.Fprintf(&b, "lv    %d\n", p.Level)
	b.WriteString(Bar("hp  ", p.HP, p.MaxHP, 10) + "\n")
	b.WriteString(Bar("mana", p.Mana, p.MaxMana, 10) + "\n")
	fmt.Fprintf(&b, "pos   %d,%d\n", p.X, p.Y)
	fmt.Fprintf(&b, "id    %s", shortFP(p.Fingerprint))
	return Panel{Title: "stats", Content: b.String()}.Render()
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
