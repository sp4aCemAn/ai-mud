package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// unimplemented is the holding screen for everything the next phases
// will build (world entry, character persistence, …).
type unimplemented struct {
	width  int
	height int
}

func newUnimplemented() unimplemented {
	return unimplemented{}
}

func (u unimplemented) Init() tea.Cmd { return nil }

func (u unimplemented) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.width = msg.Width
		u.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return u, tea.Quit
		case "esc":
			return u, gotoScreen(ScreenLanding)
		}
	}
	return u, nil
}

func (u unimplemented) View() string {
	var b strings.Builder
	b.WriteString("─ ─ ─  U N D E V E L O P E D  ─ ─ ─\n\n")
	b.WriteString("this part of the world hasn't been built yet.\n\n")
	b.WriteString("next up: world generation & entry\n\n")
	b.WriteString("[esc] back to landing · [q] quit")
	return frame(u.width, u.height, b.String())
}
