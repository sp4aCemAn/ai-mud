package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

// authScreen replays the connect-time auth sequence for the player.
//
// NOTE (template): the actual Provider calls already happened in the SSH
// handler before this screen exists — this is the visible narration of
// them, paced so the eventual real flow (lookup / register / login) is
// already shaped. When auth becomes real, these steps become live calls.
type authScreen struct {
	id      auth.Identity
	isNew   bool // true when the key was registered during connect
	steps   []string
	step    int // active step index
	ticks   int // spinner ticks since last advance
	spinner spinner.Model
	width   int
	height  int
}

func newAuthScreen(id auth.Identity) authScreen {
	s := authScreen{
		id:      id,
		isNew:   id.Fingerprint != "" && id.User.Name == "guest",
		spinner: spinner.New(spinner.WithSpinner(spinner.Meter)),
	}
	if id.Fingerprint == "" {
		s.steps = []string{
			"no key presented — anonymous connection",
			"assigning guest account",
			"ready",
		}
	} else {
		s.steps = []string{
			fmt.Sprintf("reading key %s…", id.Fingerprint),
			"checking for an existing account…",
			"creating account for this key…",
			"ready",
		}
	}
	return s
}

func (s authScreen) Init() tea.Cmd { return s.spinner.Tick }

func (s authScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, gotoScreen(ScreenLanding)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(msg)
		s.ticks++
		// ~10 ticks per step keeps the narration readable
		if s.ticks%10 == 0 && s.step < len(s.steps)-1 {
			s.step++
		} else if s.step == len(s.steps)-1 && s.ticks%10 == 0 {
			return s, gotoScreen(ScreenUnimplemented)
		}
		return s, cmd
	}
	return s, nil
}

func (s authScreen) View() string {
	var b strings.Builder
	b.WriteString("authenticating\n\n")
	for i, step := range s.steps {
		switch {
		case i < s.step:
			fmt.Fprintf(&b, "  ✓ %s\n", step)
		case i == s.step:
			fmt.Fprintf(&b, "  %s %s\n", s.spinner.View(), step)
		default:
			fmt.Fprintf(&b, "  ○ %s\n", step)
		}
	}
	b.WriteString("\n(continuing shortly…)")
	return frame(s.width, s.height, b.String())
}
