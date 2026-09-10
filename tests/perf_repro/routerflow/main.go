package main

// Repro 2: our router/value-receiver flow shape — two screens, a
// GotoMsg transition (spinner narration → "game"), stateTick-ish
// tea.Tick chains, altscreen. If this stalls at the transition while
// repro 1 (mini) ticks fine, the bug is our routing shape, not the
// stack. Debug aid only — delete when the mystery dies.

import (
	"fmt"
	"github.com/charmbracelet/wish/logging"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"
)

type GotoMsg struct{ id int }
type stateTickMsg struct{}

func gotoScreen(id int) tea.Cmd {
	return func() tea.Msg { return GotoMsg{id} }
}

func stateTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return stateTickMsg{} })
}

type reproAuth struct {
	spinner spinner.Model
	step    int
	ticks   int
}

func (a reproAuth) Init() tea.Cmd { return a.spinner.Tick }
func (a reproAuth) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		a.ticks++
		fmt.Printf("[auth] tick %d step %d\n", a.ticks, a.step)
		if a.ticks%10 == 0 {
			a.step++
		}
		if a.step >= 2 {
			return a, gotoScreen(2)
		}
		return a, cmd
	}
	return a, nil
}
func (a reproAuth) View() string { return fmt.Sprintf("auth step %d\n", a.step) }

type reproGame struct{ n int }

func (g reproGame) Init() tea.Cmd { return stateTick() }
func (g reproGame) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case stateTickMsg:
		g.n++
		fmt.Printf("[game] state tick %d\n", g.n)
		return g, stateTick()
	case tea.KeyMsg:
		return g, tea.Quit
	}
	return g, nil
}
func (g reproGame) View() string { return fmt.Sprintf("game ticks=%d\n", g.n) }

type reproLanding struct{}

func (l reproLanding) Init() tea.Cmd { return nil }
func (l reproLanding) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg:
		return l, gotoScreen(1)
	}
	return l, nil
}
func (l reproLanding) View() string { return "press any key\n" }

type reproRouter struct{ sc tea.Model }

func (r reproRouter) Init() tea.Cmd { return r.screen().Init() }

func (r reproRouter) screen() tea.Model { return r.sc }

func (r reproRouter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if got, ok := msg.(GotoMsg); ok {
		switch got.id {
		case 1:
			r.sc = reproAuth{spinner: spinner.New(spinner.WithSpinner(spinner.Meter))}
		case 2:
			r.sc = reproGame{}
		}
		return r, r.screen().Init()
	}
	m, cmd := r.sc.Update(msg)
	r.sc = m
	return r, cmd
}

func (r reproRouter) View() string { return r.screen().View() }

func main() {
	fmt.Println("routerflow listening on :2527")
	s, _ := wish.NewServer(
		wish.WithAddress("127.0.0.1:2527"),
		wish.WithHostKeyPath(".ssh/minik"),
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			bm.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				return reproRouter{sc: reproLanding{}}, []tea.ProgramOption{tea.WithAltScreen()}
			}),
			logging.Middleware(),
		),
	)
	s.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{NoClientAuth: true}
	}
	_ = s.ListenAndServe()
}
