package main

// Minimal repro: a standalone wish+bubbletea server on :2526 with a
// 1-second tick counter. If ticks flow here, the bubbletea/wish stack
// is healthy and the freeze is ours; if they don't, it's the stack.
// Debug aid only — delete when the mystery dies.

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"
)

type tickMsg struct{}
type mini struct{ n int }

func (m mini) Init() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m mini) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tickMsg:
		m.n++
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
	case tea.KeyMsg:
		return m, tea.Quit
	}
	return m, nil
}
func (m mini) View() string { return fmt.Sprintf("t=%d\n", m.n) }

func main() {
	s, _ := wish.NewServer(
		wish.WithAddress("127.0.0.1:2526"),
		wish.WithHostKeyPath(".ssh/minik"),
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			bm.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				return mini{}, nil
			}),
		),
	)
	s.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{NoClientAuth: true}
	}
	_ = s.ListenAndServe()
}
