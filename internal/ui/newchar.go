package ui

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

// namePattern: starts with a letter, 3–16 chars of letters/digits.
var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{2,15}$`)

// ValidName reports whether a character name is acceptable.
func ValidName(name string) bool { return namePattern.MatchString(name) }

var classes = []string{"Warrior", "Rogue", "Mage"}

const (
	stepName = iota
	stepClass
	stepConfirm
)

// charWizard is the new-character flow: name → class → confirm.
// On confirm the result is logged (persistence comes with auth/world).
type charWizard struct {
	id      auth.Identity
	step    int
	input   textinput.Model
	classIx int
	name    string
	errMsg  string
	width   int
	height  int
}

func newCharWizard(id auth.Identity) charWizard {
	ti := textinput.New()
	ti.Placeholder = "character name"
	ti.CharLimit = 16
	ti.Focus()
	return charWizard{id: id, input: ti}
}

func (w charWizard) Init() tea.Cmd { return textinput.Blink }

func (w charWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w.width = msg.Width
		w.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return w, tea.Quit
		case "esc":
			if w.step == stepName {
				return w, gotoScreen(ScreenLanding)
			}
			w.step--
			w.errMsg = ""
			if w.step == stepName {
				w.input.Focus()
			}
			return w, nil
		}

		switch w.step {
		case stepName:
			switch msg.String() {
			case "enter":
				name := strings.TrimSpace(w.input.Value())
				if !ValidName(name) {
					w.errMsg = "name must be 3–16 chars, start with a letter, letters/digits only"
					return w, nil
				}
				w.name = name
				w.step = stepClass
				w.input.Blur()
				w.errMsg = ""
				return w, nil
			}
			var cmd tea.Cmd
			w.input, cmd = w.input.Update(msg)
			return w, cmd

		case stepClass:
			switch msg.String() {
			case "up", "k":
				if w.classIx > 0 {
					w.classIx--
				}
			case "down", "j":
				if w.classIx < len(classes)-1 {
					w.classIx++
				}
			case "enter":
				w.step = stepConfirm
			case "1", "2", "3":
				w.classIx = int(msg.String()[0] - '1')
			}

		case stepConfirm:
			switch msg.String() {
			case "y", "enter":
				slog.Info("character created (not persisted yet)",
					"name", w.name,
					"class", classes[w.classIx],
					"fingerprint", w.id.Fingerprint,
					"account", w.id.User.Name)
				return w, gotoScreen(ScreenUnimplemented)
			case "n":
				return newCharWizard(w.id), textinput.Blink // restart wizard
			}
		}
	}
	return w, nil
}

func (w charWizard) View() string {
	var b strings.Builder

	switch w.step {
	case stepName:
		b.WriteString("create a new character — step 1/3\n\n")
		b.WriteString("what shall we call you?\n\n")
		b.WriteString(w.input.View() + "\n")
		if w.errMsg != "" {
			fmt.Fprintf(&b, "\n✗ %s\n", w.errMsg)
		}
		b.WriteString("\nenter confirm · esc back to menu")

	case stepClass:
		fmt.Fprintf(&b, "create a new character — step 2/3\n\n")
		b.WriteString("choose a class:\n\n")
		for i, c := range classes {
			marker := "  "
			if i == w.classIx {
				marker = "▸ "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, c)
		}
		b.WriteString("\n↑/↓ move · enter select · esc back")

	case stepConfirm:
		fmt.Fprintf(&b, "create a new character — step 3/3\n\n")
		fmt.Fprintf(&b, "  name:  %s\n", w.name)
		fmt.Fprintf(&b, "  class: %s\n\n", classes[w.classIx])
		b.WriteString("create this character?\n\n")
		b.WriteString("y create · n start over · esc edit class")
	}

	return frame(w.width, w.height, b.String())
}
