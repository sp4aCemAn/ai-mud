package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

// loginScreen: name + password for a returning player whose hands
// wander between machines. On success the account owns this
// connection's key too — the fingerprint is now one of its
// credentials, and the session lands in the world as that account.
type loginScreen struct {
	accts *auth.Accounts
	id    auth.Identity

	nameIn, passIn textinput.Model
	focusIx        int // 0 = name, 1 = password
	errMsg         string
	width, height  int
}

func newLoginScreen(accts *auth.Accounts, id auth.Identity) loginScreen {
	name := textinput.New()
	name.Placeholder = "account name"
	name.CharLimit = 32
	name.Focus()

	pass := textinput.New()
	pass.Placeholder = "password"
	pass.EchoMode = textinput.EchoPassword
	pass.EchoCharacter = '•'

	return loginScreen{accts: accts, id: id, nameIn: name, passIn: pass}
}

func (l loginScreen) Init() tea.Cmd { return textinput.Blink }

func (l loginScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		l.width = msg.Width
		l.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return l, tea.Quit
		case "esc":
			return l, gotoScreen(ScreenLanding)
		case "tab", "down":
			l.focusIx = (l.focusIx + 1) % 2
			l.applyFocus()
		case "up":
			l.focusIx = (l.focusIx + 1) % 2
			l.applyFocus()
		case "enter":
			return l.submit()
		}

		// route input into the focused field
		var cmd tea.Cmd
		if l.focusIx == 0 {
			l.nameIn, cmd = l.nameIn.Update(msg)
		} else {
			l.passIn, cmd = l.passIn.Update(msg)
		}
		return l, cmd
	}
	return l, nil
}

// applyFocus syncs the two inputs' focus state with focusIx —
// textinput.Update drops keystrokes while blurred, so this is the
// authority on who receives typing.
func (l *loginScreen) applyFocus() {
	if l.focusIx == 1 {
		l.nameIn.Blur()
		l.passIn.Focus()
	} else {
		l.passIn.Blur()
		l.nameIn.Focus()
	}
}

// submit validates credentials; on success the connection's credential
// attaches and the router lands in the world as the account.
func (l loginScreen) submit() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(l.nameIn.Value())
	password := l.passIn.Value()
	if name == "" || password == "" {
		l.errMsg = "both a name and a password, please"
		if name == "" {
			l.passIn.Blur()
			l.nameIn.Focus()
			l.focusIx = 0
		}
		return l, nil
	}

	acc, err := l.accts.Login(name, password, "ssh", l.id.Fingerprint)
	if err != nil {
		l.errMsg = "no such account — or the password slipped"
		return l, nil
	}
	id := l.accts.IdentityForAccount(acc)
	id.Fingerprint = l.id.Fingerprint
	return l, swapIdentity(id, ScreenGame)
}

func (l loginScreen) View() string {
	var b strings.Builder
	b.WriteString("log in\n\n")
	b.WriteString("welcome back, denizen.\n\n")

	b.WriteString(l.nameIn.View() + "\n")
	b.WriteString(l.passIn.View() + "\n")

	if l.errMsg != "" {
		fmt.Fprintf(&b, "\n✗ %s\n", l.errMsg)
	}

	b.WriteString("\nenter log in · tab next field · esc back")
	return frame(l.width, l.height, b.String())
}
