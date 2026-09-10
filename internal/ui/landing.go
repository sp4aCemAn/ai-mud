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

// landingOptions adapts to the session state: a guest gets the plain
// menu; a real account swaps "Join World" for "Play as <name>".
func landingOptions(id auth.Identity) []landingOption {
	if id.User.Name != "guest" {
		return []landingOption{
			{key: "1", label: "Play as " + id.User.Name, id: ScreenAuth},
			{key: "2", label: "New Character", id: ScreenNewChar},
			{key: "3", label: "Switch account", id: ScreenLogin},
		}
	}
	return []landingOption{
		{key: "1", label: "Join World", id: ScreenAuth},
		{key: "2", label: "New Character", id: ScreenNewChar},
		{key: "3", label: "Log in", id: ScreenLogin},
	}
}

// Landing is the first screen a connected player sees.
type Landing struct {
	id     auth.Identity
	accts  *auth.Accounts
	width  int
	height int
	cursor int
}

func newLanding(id auth.Identity, accts *auth.Accounts) Landing {
	return Landing{id: id, accts: accts}
}

func (l Landing) Init() tea.Cmd { return nil }

func (l Landing) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.width = msg.Width
		l.height = msg.Height
	case tea.KeyMsg:
		opts := landingOptions(l.id)
		switch msg.String() {
		case "q", "esc":
			return l, tea.Quit
		case "up", "k":
			if l.cursor > 0 {
				l.cursor--
			}
		case "down", "j":
			if l.cursor < len(opts)-1 {
				l.cursor++
			}
		case "enter":
			return l, gotoScreen(opts[l.cursor].id)
		default:
			// direct-select keys
			for i, opt := range opts {
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
	if l.id.Fingerprint == "" {
		b.WriteString("(anonymous — no SSH key presented)\n")
	}

	b.WriteString("\n")
	opts := landingOptions(l.id)
	if l.cursor >= len(opts) {
		l.cursor = len(opts) - 1
	}
	for i, opt := range opts {
		marker := "  "
		if i == l.cursor {
			marker = "▸ "
		}
		fmt.Fprintf(&b, "%s[%s] %s\n", marker, opt.key, opt.label)
	}

	b.WriteString("\n↑/↓ move · enter select · q quit")
	return frame(l.width, l.height, b.String())
}
