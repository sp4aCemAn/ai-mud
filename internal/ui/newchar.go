package ui

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

// namePattern: starts with a letter, 3–16 chars of letters/digits.
var namePattern = regexp.MustCompile(`^\p{L}[\p{L}\p{N} ]{2,15}$`)

// ValidName reports whether a character name is acceptable. Spaces
// are welcome inside ("Aldric the Older"); leading/trailing digits
// and symbol noise are world-hostile and stay out.
func ValidName(name string) bool {
	return namePattern.MatchString(name) && !strings.Contains(name, "  ")
}

var classes = []string{"Warrior", "Rogue", "Mage"}

const (
	stepName = iota
	stepClass
	stepConfirm
	stepReveal // fresh account: the one-time password showing
)

// charWizard is the new-character flow: name → class → confirm →
// (new account) the one-time password reveal → the world. The fresh
// account case is what a brand-new key/silver-tongued guest lands in
// — the reveal is the verify-now nudge in its most natural spot.
type charWizard struct {
	id      auth.Identity
	accts   *auth.Accounts
	step    int
	input   textinput.Model
	classIx int
	name    string
	reveal  string // the generated password's only appearance
	errMsg  string
	width   int
	height  int
}

func newCharWizard(id auth.Identity, accts *auth.Accounts) charWizard {
	ti := textinput.New()
	ti.Placeholder = "character name"
	ti.CharLimit = 16
	ti.Focus()
	return charWizard{id: id, accts: accts, input: ti}
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
					w.errMsg = "names are 3–16 chars, start with a letter, letters/digits/spaces only"
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
				return w.commit()
			case "n":
				return newCharWizard(w.id, w.accts), textinput.Blink // restart wizard
			}

		case stepReveal:
			switch msg.String() {
			case "c":
				// best-effort OSC 52 clipboard write — terminals that
				// don't speak it just ignore the escape; the password
				// is on screen regardless
				return w, tea.Printf("\x1b]52;c;%s\x07", osc52(w.reveal))
			case "enter":
				return w.enterWorld()
			}
		}
	}
	return w, nil
}

// commit: anonymous sessions must mint a named account and reveal its
// one-time password (the password is their only credential). Keyed
// sessions — fresh or not — are already verified by the handshake, so
// they walk straight into the world; a password stays optional, set
// later with [v] in the game.
func (w charWizard) commit() (tea.Model, tea.Cmd) {
	if w.accts == nil {
		// invariant: the router injects accounts; stubbed tests move on
		return w, gotoScreen(ScreenGame)
	}

	if w.id.Fingerprint != "" {
		return w, swapIdentity(w.id, ScreenGame)
	}

	_, generated, err := w.accts.CreateNamed(w.name)
	if err != nil {
		w.errMsg = "that name is already taken — pick another"
		w.step = stepName
		w.input.Focus()
		return w, nil
	}
	w.reveal = generated
	w.step = stepReveal
	return w, nil
}

// enterWorld acks the revealed password (seen = kept) and lands in
// the world under an upgraded identity.
func (w charWizard) enterWorld() (tea.Model, tea.Cmd) {
	var id auth.Identity
	if w.id.AccountFresh {
		id = w.id
		id.Verified = true
		id.NewPassword = ""
		id.AccountFresh = false
		if err := w.accts.AckPassword(w.id.User.Name); err != nil {
			// don't block gameplay on the ack
		}
	} else {
		if err := w.accts.AckPassword(w.name); err != nil {
			// ack failure shouldn't strand the player on the reveal
		}
		acc, err := w.accts.Account(w.name) // fetch AFTER the ack: the ack sets PasswordAcked
		if err != nil {
			return w, gotoScreen(ScreenGame) // fallback: play anyway
		}
		id = w.accts.IdentityForAccount(acc)
		id.AccountFresh = false
	}
	id.Fingerprint = w.id.Fingerprint
	return w, swapIdentity(id, ScreenGame)
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
		b.WriteString("\nenter confirm · esc back to the menu")

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

	case stepReveal:
		b.WriteString("one-time password\n\n")
		b.WriteString("this password logs your body in from any")
		b.WriteString(" machine — this is the ONLY time it's shown.\n\n")
		fmt.Fprintf(&b, "  %s\n\n", w.reveal)
		b.WriteString("[c]opy to clipboard · enter marks it kept")
	}

	return frame(w.width, w.height, b.String())
}

// osc52 encodes a string for the OSC 52 clipboard escape.
func osc52(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
