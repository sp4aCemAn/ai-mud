package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
)

func testIdentity() auth.Identity {
	return auth.Identity{
		Fingerprint: "SHA256:testfp",
		User:        auth.User{ID: "guest", Name: "guest"},
	}
}

var specialKeys = map[string]tea.KeyType{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"backspace": tea.KeyBackspace,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"ctrl+c":    tea.KeyCtrlC,
}

func keyMsg(k string) tea.KeyMsg {
	if kt, ok := specialKeys[k]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// drive feeds key presses to the router, resolving transition cmds.
// Returns the (possibly new) router model.
func drive(t *testing.T, r tea.Model, keys ...string) tea.Model {
	t.Helper()
	for _, k := range keys {
		m, cmd := r.Update(keyMsg(k))
		r = m
		if cmd != nil {
			// execute transition cmds immediately (like bubbletea would)
			if msg := cmd(); msg != nil {
				m, _ = r.Update(msg)
				r = m
			}
		}
	}
	return r
}

func activeScreen(r Router) tea.Model { return r.screen }

func TestRouterLandingNavigation(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())

	// [1] → auth screen
	r = drive(t, r, "1")
	if _, ok := activeScreen(r.(Router)).(authScreen); !ok {
		t.Fatalf("key 1 should route to auth screen, got %T", activeScreen(r.(Router)))
	}

	// back to landing, [2] → wizard
	r = drive(t, r, "esc", "esc", "2")
	if _, ok := activeScreen(r.(Router)).(charWizard); !ok {
		t.Fatalf("key 2 should route to wizard, got %T", activeScreen(r.(Router)))
	}

	// cursor navigation + enter: down twice then enter → still New Character
	r = drive(t, r, "esc", "esc", "down", "down", "enter")
	if _, ok := activeScreen(r.(Router)).(charWizard); !ok {
		t.Fatalf("cursor+enter should clamp and route to wizard, got %T", activeScreen(r.(Router)))
	}
}

func TestWizardFullWalk(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())
	r = drive(t, r, "2")

	// invalid name rejected (stays on name step)
	r = drive(t, r, "a", "b", "enter")
	w := activeScreen(r.(Router)).(charWizard)
	if w.step != stepName || w.errMsg == "" {
		t.Fatalf("invalid name should stay on stepName with error, step=%d err=%q", w.step, w.errMsg)
	}

	// valid name → class → confirm → unimplemented
	r = drive(t, r, "backspace", "backspace", "T", "h", "o", "r", "enter", "enter", "y")
	if _, ok := activeScreen(r.(Router)).(unimplemented); !ok {
		t.Fatalf("finished wizard should land on unimplemented, got %T", activeScreen(r.(Router)))
	}
}

func TestWizardEscBacksOut(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())
	r = drive(t, r, "2", "T", "h", "o", "r", "enter") // name → class
	r = drive(t, r, "esc")                            // class → name
	if w := activeScreen(r.(Router)).(charWizard); w.step != stepName {
		t.Fatalf("esc on class step should go back to name, step=%d", w.step)
	}
	r = drive(t, r, "esc")
	if _, ok := activeScreen(r.(Router)).(Landing); !ok {
		t.Fatalf("esc on name step should return to landing, got %T", activeScreen(r.(Router)))
	}
}

func TestWizardRestartWithN(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())
	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "n")
	w := activeScreen(r.(Router)).(charWizard)
	if w.step != stepName || w.name != "" {
		t.Fatalf("'n' should restart the wizard, step=%d name=%q", w.step, w.name)
	}
}

// pump feeds msg to the router and executes every returned cmd
// recursively (a mini bubbletea runtime) until the queue drains or the
// message budget is hit. Note the queue must fully drain: a transition
// cmd produced by the last message would otherwise be lost.
func pump(t *testing.T, r tea.Model, first tea.Msg, budget int) tea.Model {
	t.Helper()
	msgs := []tea.Msg{first}
	processed := 0
	for len(msgs) > 0 && processed < budget {
		msg := msgs[0]
		msgs = msgs[1:]
		processed++
		m, cmd := r.Update(msg)
		r = m
		if cmd != nil {
			if got := cmd(); got != nil {
				msgs = append(msgs, got)
			}
		}
	}
	return r
}

func TestAuthScreenWalksToUnimplemented(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())
	r = drive(t, r, "1")
	if _, ok := r.(Router).screen.(authScreen); !ok {
		t.Fatal("key 1 should open the auth screen")
	}

	r = pump(t, r, spinner.TickMsg{}, 300)
	if _, ok := r.(Router).screen.(unimplemented); !ok {
		t.Fatal("auth screen never reached unimplemented")
	}
}

func TestUnimplementedEscReturnsToLanding(t *testing.T) {
	var r tea.Model = NewRouter(testIdentity())
	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "y", "esc")
	if _, ok := activeScreen(r.(Router)).(Landing); !ok {
		t.Fatalf("esc on unimplemented should return to landing, got %T", activeScreen(r.(Router)))
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"Thor", "xena1", "A0b9"}
	invalid := []string{"", "ab", "has space", "1leading", "waytoolongnameaaaa", "sym!"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestLandingViewShowsIdentity(t *testing.T) {
	var l tea.Model = NewRouter(testIdentity())
	l, _ = l.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	v := l.View()
	if !strings.Contains(v, "guest") {
		t.Error("landing should show connected identity")
	}
	if !strings.Contains(v, "Join World") || !strings.Contains(v, "New Character") {
		t.Error("landing should show both menu options")
	}
	if !strings.Contains(v, "█████╗") {
		t.Error("landing should render the ASCII logo")
	}
}
