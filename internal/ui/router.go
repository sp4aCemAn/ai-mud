// Package ui holds the bubbletea screens a player moves through over SSH.
//
// Structure: a Router (finite state machine) owns the active screen and
// delegates Update/View to it. Screens never import each other — they ask
// for transitions by emitting GotoMsg; only the router knows the map.
package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/game"
)

// ScreenID enumerates the screens in the user loop.
type ScreenID int

const (
	ScreenLanding ScreenID = iota
	ScreenAuth
	ScreenLogin
	ScreenNewChar
	ScreenGame
	ScreenUnimplemented
)

// GotoMsg asks the router to switch to the given screen.
type GotoMsg struct{ ID ScreenID }

func gotoScreen(id ScreenID) tea.Cmd {
	return func() tea.Msg { return GotoMsg{ID: id} }
}

// identitySwapMsg replaces the session identity (login / rename /
// verification) and lands on a screen in one move — screens can't
// reach into the router directly.
type identitySwapMsg struct {
	id   auth.Identity
	next ScreenID
}

func swapIdentity(id auth.Identity, next ScreenID) tea.Cmd {
	return func() tea.Msg { return identitySwapMsg{id: id, next: next} }
}

// Router is the top-level model for one SSH session.
type Router struct {
	identity auth.Identity
	accts    *auth.Accounts  // account service (never nil)
	pc       game.PlayerView // the world client (nil = UI-only, tests)
	screen   tea.Model
	width    int
	height   int

	initCmd tea.Cmd
}

func NewRouter(id auth.Identity, pc game.PlayerView) Router {
	return Router{identity: id, accts: auth.NewAccounts(nil), pc: pc, screen: newLanding(id, auth.NewAccounts(nil))}
}

// NewRouterDirectGame skips landing/auth — straight into the world
// (perf-diag harness: narrows whether the auth screen stalls the flow).
func NewRouterDirectGame(id auth.Identity, pc game.PlayerView, accts *auth.Accounts) Router {
	if accts == nil {
		accts = auth.NewAccounts(nil)
	}
	r := Router{identity: id, accts: accts, pc: pc}
	sc, scInit := r.screenFor(ScreenLanding)
	r.screen = sc
	r.initCmd = scInit
	return r
}

func NewRouterWithAccounts(id auth.Identity, pc game.PlayerView, accts *auth.Accounts) Router {
	if accts == nil {
		accts = auth.NewAccounts(nil)
	}
	return Router{identity: id, accts: accts, pc: pc, screen: newLanding(id, accts)}
}

func (r Router) Init() tea.Cmd {
	if r.initCmd != nil {
		return r.initCmd
	}
	return r.screen.Init()
}

func (r Router) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// transitions first — screens emit these, router owns the map.
	// The new screen's Init must run: that's where cmds like the
	// spinner's first tick / text cursor blink get scheduled.
	if got, ok := msg.(GotoMsg); ok {
		sc, scInit := r.screenFor(got.ID)
		r.screen = sc
		return r, scInit
	}

	// identity swaps re-issue the current screen with upgraded state
	if got, ok := msg.(identitySwapMsg); ok {
		r.identity = got.id
		sc, scInit := r.screenFor(got.next)
		r.screen = sc
		return r, scInit
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width = msg.Width
		r.height = msg.Height
	case tea.KeyMsg:
		if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))) {
			// the game screen owns quit-guarding (unverified must
			// be warned); other screens quit immediately
			if _, isGame := r.screen.(GameScreen); isGame {
				m, cmd := r.screen.Update(msg)
				r.screen = m
				return r, cmd
			}
			return r, tea.Quit
		}
	}

	// delegate to the active screen
	m, cmd := r.screen.Update(msg)
	r.screen = m
	return r, cmd
}

func (r Router) View() string {
	return r.screen.View()
}

// screenFor builds a fresh screen. New screens are immediately sized so
// they render correctly even though the initial WindowSizeMsg has passed.
func (r Router) screenFor(id ScreenID) (tea.Model, tea.Cmd) {
	var m tea.Model
	switch id {
	case ScreenLanding:
		m = newLanding(r.identity, r.accts)
	case ScreenAuth:
		m = newAuthScreen(r.identity)
	case ScreenLogin:
		m = newLoginScreen(r.accts, r.identity)
	case ScreenNewChar:
		m = newCharWizard(r.identity, r.accts)
	case ScreenGame:
		m = newGameScreen(r.identity, r.pc)
	case ScreenUnimplemented:
		m = newUnimplemented()
	default:
		m = newUnimplemented()
	}

	init := m.Init()
	if r.width > 0 {
		// prime the size like the first WindowSizeMsg would; the cmd
		// matters — for GameScreen it schedules the debounced resize
		var prime tea.Cmd
		m, prime = m.Update(tea.WindowSizeMsg{Width: r.width, Height: r.height})
		if prime != nil {
			init = tea.Batch(init, prime)
		}
	}
	return m, init
}

// --- shared style helpers ---------------------------------------------------

var boxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	Padding(1, 3)

// frame centers content in the terminal, wrapped in a rounded box. Before
// the first resize (width==0) it degrades to bare content.
func frame(width, height int, content string) string {
	if width == 0 {
		return content
	}
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		boxStyle.Render(content),
	)
}
