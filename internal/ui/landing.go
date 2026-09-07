package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

// logo is the landing screen title. ASCII art is required (per design).
const logo = `
 █████╗ ██╗    ██╗   ██╗██████╗
██╔══██╗██║    ██║   ██║██╔══██╗
███████║██║    ██║   ██║██████╔╝
██╔══██║██║    ╚██╗ ██╔╝██╔══██╗
██║  ██║███████╗╚████╔╝ ██║  ██║
╚═╝  ╚═╝╚══════╝ ╚═══╝  ╚═╝  ╚═╝`

type landingOption struct {
	key   string // direct-select key
	label string
	id    ScreenID
}

var landingOptions = []landingOption{
	{key: "1", label: "Join World", id: ScreenAuth},
	{key: "2", label: "New Character", id: ScreenNewChar},
}

// Landing is the first screen a connected player sees.
type Landing struct {
	id     auth.Identity
	width  int
	height int
	cursor int
}

func newLanding(id auth.Identity) Landing {
	return Landing{id: id}
}

func (l Landing) Init() tea.Cmd { return nil }

func (l Landing) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.width = msg.Width
		l.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return l, tea.Quit
		case "up", "k":
			if l.cursor > 0 {
				l.cursor--
			}
		case "down", "j":
			if l.cursor < len(landingOptions)-1 {
				l.cursor++
			}
		case "enter":
			return l, gotoScreen(landingOptions[l.cursor].id)
		default:
			// direct-select keys
			for i, opt := range landingOptions {
				if msg.String() == opt.key {
					l.cursor = i
					return l, gotoScreen(opt.id)
				}
			}
		}
	}
	return l, nil
}

func (l Landing) View() string {
	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "connected as %s\n", l.id.User.Name)

	b.WriteString("\n")
	for i, opt := range landingOptions {
		marker := "  "
		if i == l.cursor {
			marker = "▸ "
		}
		fmt.Fprintf(&b, "%s[%s] %s\n", marker, opt.key, opt.label)
	}

	b.WriteString("\n↑/↓ move · enter select · q quit")
	return frame(l.width, l.height, b.String())
}
