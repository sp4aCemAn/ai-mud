package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/game"
)

func testIdentity() auth.Identity {
	return auth.Identity{
		Fingerprint: "SHA256:testfp",
		User:        auth.User{ID: "guest", Name: "guest"},
		Verified:    true, // key-backed sessions verify in the handshake
	}
}

// anonTestIdentity is a key-less guest — no credential, password-only
// account creation path.
func anonTestIdentity() auth.Identity {
	return auth.Identity{Fingerprint: "", User: auth.User{ID: "guest", Name: "guest"}}
}

// newTestRouter wires the router to a real in-process game server, so
// the gameplay flow is exercised end to end without any transport.
func newTestRouter() (tea.Model, *game.Server) {
	stateTickEvery = time.Millisecond // don't sleep in tests
	world := game.NewServer()
	world.DebugFlattenWorld() // deterministic terrain for positional asserts
	return NewRouter(testIdentity(), world), world
}

// newTestRouterWith is the same, with a chosen identity.
func newTestRouterWith(id auth.Identity) (tea.Model, *game.Server) {
	stateTickEvery = time.Millisecond
	world := game.NewServer()
	world.DebugFlattenWorld()
	return NewRouter(id, world), world
}

var specialKeys = map[string]tea.KeyType{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"backspace": tea.KeyBackspace,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"ctrl+c":    tea.KeyCtrlC,
}

func keyMsg(k string) tea.KeyMsg {
	if kt, ok := specialKeys[k]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// drive feeds key presses to the router, resolving transition cmds.
func drive(t *testing.T, r tea.Model, keys ...string) tea.Model {
	t.Helper()
	for _, k := range keys {
		m, cmd := r.Update(keyMsg(k))
		r = m
		if cmd != nil {
			if msg := cmd(); msg != nil {
				m, _ = r.Update(msg)
				r = m
			}
		}
	}
	return r
}

// pump feeds msg to the router and executes every returned cmd
// recursively (a mini bubbletea runtime) until the queue drains or the
// message budget is hit. The queue must fully drain: a transition cmd
// produced by the last message would otherwise be lost.
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

func activeScreen(r Router) tea.Model { return r.screen }

// --- router / navigation ----------------------------------------------------

func TestRouterLandingNavigation(t *testing.T) {
	r, _ := newTestRouter()

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

	// back to landing, one cursor step down then enter → wizard again
	r = drive(t, r, "esc", "esc", "down", "enter")
	if _, ok := activeScreen(r.(Router)).(charWizard); !ok {
		t.Fatalf("down+enter should route to wizard, got %T", activeScreen(r.(Router)))
	}

	// cursor down twice → past wizard, onto the login screen
	r = drive(t, r, "esc", "esc", "down", "down", "enter")
	if _, ok := activeScreen(r.(Router)).(loginScreen); !ok {
		t.Fatalf("two downs should reach the login screen, got %T", activeScreen(r.(Router)))
	}
}

func TestAuthScreenRouteAEndsInGame(t *testing.T) {
	r, world := newTestRouter()
	r = drive(t, r, "1")

	r = pump(t, r, spinner.TickMsg{}, 300)
	gs, ok := activeScreen(r.(Router)).(GameScreen)
	if !ok {
		t.Fatalf("route A should end in the game screen, got %T", activeScreen(r.(Router)))
	}
	if _, ok := world.State(testIdentity().Fingerprint); !ok {
		t.Fatal("entering the game should have joined the player to the world")
	}
	if gs.p.Name != "guest" {
		t.Fatalf("player should be named after identity, got %q", gs.p.Name)
	}
}

// --- character wizard --------------------------------------------------------

func TestWizardFullWalk(t *testing.T) {
	// anonymous session: minting an account — the password reveal is
	// mandatory (it's the body's only credential)
	r, _ := newTestRouterWith(anonTestIdentity())
	r = drive(t, r, "2")

	// invalid name rejected (stays on name step)
	r = drive(t, r, "a", "b", "enter")
	w := activeScreen(r.(Router)).(charWizard)
	if w.step != stepName || w.errMsg == "" {
		t.Fatalf("invalid name should stay on stepName with error, step=%d err=%q", w.step, w.errMsg)
	}

	// valid name → class → confirm → the one-time password reveal
	r = drive(t, r, "backspace", "backspace", "T", "h", "o", "r", "enter", "enter", "y")
	w = activeScreen(r.(Router)).(charWizard)
	if w.step != stepReveal {
		t.Fatalf("finished wizard should show the password reveal, step=%d", w.step)
	}
	if w.reveal == "" || len(w.reveal) != 25 {
		t.Fatalf("reveal must carry the 25-char generated password: %q", w.reveal)
	}

	// enter marks it kept and lands in the world
	r = drive(t, r, "enter")
	if _, ok := activeScreen(r.(Router)).(GameScreen); !ok {
		t.Fatalf("after the reveal, enter should land in the game, got %T", activeScreen(r.(Router)))
	}
	gs := activeScreen(r.(Router)).(GameScreen)
	if gs.id.User.Name != "Thor" || !gs.id.Verified {
		t.Fatalf("identity should be the new account, verified: %+v", gs.id)
	}
}

func TestWizardKeyedSkipsReveal(t *testing.T) {
	// keyed fresh session: the handshake already verified the player —
	// no password requirement, straight into the world
	id := testIdentity()
	id.AccountFresh = true
	id.NewPassword = "generated-never-typed-1"
	r, _ := newTestRouterWith(id)

	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "y")
	if _, ok := activeScreen(r.(Router)).(GameScreen); !ok {
		t.Fatalf("keyed sessions skip the reveal: got %T", activeScreen(r.(Router)))
	}
	if r.View() == "" {
		t.Fatal("game view should render")
	}
}

// TestWizardKeyedExisting goes straight in as well: no reveal, no mint.
func TestWizardKeyedExisting(t *testing.T) {
	id := testIdentity() // keyed, non-fresh
	id.AccountFresh = false
	r, _ := newTestRouterWith(id)
	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "y")
	if _, ok := activeScreen(r.(Router)).(GameScreen); !ok {
		t.Fatalf("keyed non-fresh sessions should enter the world, got %T", activeScreen(r.(Router)))
	}
}

func TestWizardEscBacksOut(t *testing.T) {
	r, _ := newTestRouter()
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
	r, _ := newTestRouter()
	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "n")
	w := activeScreen(r.(Router)).(charWizard)
	if w.step != stepName || w.name != "" {
		t.Fatalf("'n' should restart the wizard, step=%d name=%q", w.step, w.name)
	}
}

func TestRevealEscBacksOutToConfirm(t *testing.T) {
	// anonymous session (the reveal path)
	r, _ := newTestRouterWith(anonTestIdentity())
	r = drive(t, r, "2", "T", "h", "o", "r", "enter", "enter", "y", "esc")
	w := activeScreen(r.(Router)).(charWizard)
	if w.step != stepConfirm {
		t.Fatalf("esc on the reveal should return to confirm, step=%d", w.step)
	}
}

// --- gameplay ----------------------------------------------------------------

func gameScreenAfterJoin(t *testing.T) (tea.Model, *game.Server) {
	t.Helper()
	r, world := newTestRouter()
	r = drive(t, r, "1")
	r = pump(t, r, spinner.TickMsg{}, 300)
	world.DebugFlattenWorld() // the resize regen re-rolls terrain — flatten again
	gs, ok := activeScreen(r.(Router)).(GameScreen)
	if !ok {
		t.Fatalf("expected game screen, got %T", activeScreen(r.(Router)))
	}
	if gs.width == 0 {
		// prime it with a terminal size like bubbletea would
		m, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		r = m.(Router)
	}
	world.DebugFlattenWorld()
	return r, world
}

func TestGameMovement(t *testing.T) {
	r, world := gameScreenAfterJoin(t)
	fp := testIdentity().Fingerprint
	start, _ := world.State(fp)

	r = drive(t, r, "j") // down (hjkl)
	p, _ := world.State(fp)
	if p.Y != start.Y+1 || p.X != start.X {
		t.Fatalf("j should move down: start=(%d,%d) now=(%d,%d)", start.X, start.Y, p.X, p.Y)
	}

	r = drive(t, r, "h", "l", "k") // left, right, up → back to start
	p, _ = world.State(fp)
	if p.X != start.X || p.Y != start.Y {
		t.Fatalf("h+l+k should return to start: now=(%d,%d)", p.X, p.Y)
	}

	r = drive(t, r, "w") // wasd up
	p, _ = world.State(fp)
	if p.Y != start.Y-1 {
		t.Fatalf("w should move up: Y=%d", p.Y)
	}

	r = drive(t, r, "s", "d") // down (back), right
	p, _ = world.State(fp)
	if p.X != start.X+1 || p.Y != start.Y {
		t.Fatalf("s+d should land one east of start: now=(%d,%d)", p.X, p.Y)
	}
}

func TestGameMovementBlockedWhileInventoryOpen(t *testing.T) {
	r, world := gameScreenAfterJoin(t)
	fp := testIdentity().Fingerprint
	start, _ := world.State(fp)

	r = drive(t, r, "i") // open inventory
	gs := activeScreen(r.(Router)).(GameScreen)
	if !gs.inv {
		t.Fatal("i should open the inventory")
	}
	r = drive(t, r, "j", "h")
	p, _ := world.State(fp)
	if p.X != start.X || p.Y != start.Y {
		t.Fatalf("movement must not leak through the overlay: (%d,%d)", p.X, p.Y)
	}

	r = drive(t, r, "esc")
	gs = activeScreen(r.(Router)).(GameScreen)
	if gs.inv {
		t.Fatal("esc should close the inventory")
	}
	r = drive(t, r, "j")
	p, _ = world.State(fp)
	if p.Y != start.Y+1 {
		t.Fatalf("movement should work again after closing: Y=%d", p.Y)
	}
}

func TestGameViewRenders(t *testing.T) {
	r, _ := gameScreenAfterJoin(t)
	v := r.View()

	if !strings.Contains(v, "@") {
		t.Fatal("player dot @ missing from view")
	}
	if !strings.Contains(v, "hp") || !strings.Contains(v, "mp") || !strings.Contains(v, "⚔") {
		t.Fatal("HUD lines missing (hp/mana/vitals)")
	}
	if !strings.Contains(v, "██████████") {
		t.Fatal("full HP bar missing")
	}

	r = drive(t, r, "i")
	v = r.View()
	if !strings.Contains(v, "Pack") {
		t.Fatal("inventory window title missing when open")
	}
	r = drive(t, r, "esc")
	if strings.Contains(r.View(), "Pack") {
		t.Fatal("inventory should be gone after esc")
	}
}

func TestShortFP(t *testing.T) {
	if got := shortFP("SHA256:AbCdEf1234567890"); !strings.HasSuffix(got, "34567890") {
		t.Fatalf("long fp should truncate to tail, got %q", got)
	}
	if got := shortFP(""); got != "anonymous" {
		t.Fatalf("empty fp should say anonymous, got %q", got)
	}
}

// --- name validation ----------------------------------------------------------

func TestValidName(t *testing.T) {
	valid := []string{"Thor", "xena1", "A0b9", "grim thistle", "Fir Two"}
	invalid := []string{"", "ab", "1leading", "waytoolongnameaaaa", "sym!"}
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
	l, _ := newTestRouter()
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
