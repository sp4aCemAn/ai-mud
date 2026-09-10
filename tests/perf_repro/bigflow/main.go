package main

// Isolation repro: big rendered frame + key→counter latency measurement.
// Variant A: small view (1 line). Variant B: full-size altscreen frame.
// Env BIG=1 selects B; env FPS=[0|30]; env ALTSC=1 enables altscreen.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"
)

type tickMsg struct{}

type big struct {
	n    int
	keys int
}

func (m big) Init() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m big) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.n++
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{} })
	case tea.KeyMsg:
		_ = msg
		m.keys++
		return m, nil
	}
	_ = msg
	return m, nil
}
func (m big) View() string {
	if os.Getenv("BIG") == "" {
		return fmt.Sprintf("t=%d keys=%d\n", m.n, m.keys)
	}
	line := strings.Repeat("x", 99)
	b := strings.Builder{}
	for i := 0; i < 28; i++ {
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "t=%d keys=%d", m.n, m.keys)
	return b.String()
}

func main() {
	opts := []tea.ProgramOption{}
	if os.Getenv("ALTSC") != "" {
		opts = append(opts, tea.WithAltScreen())
	}
	if fps := os.Getenv("FPS"); fps != "" {
		n, err := strconv.Atoi(fps)
		if err == nil && n > 0 {
			opts = append(opts, tea.WithFPS(n))
		}
	}
	s, _ := wish.NewServer(
		wish.WithAddress("127.0.0.1:2526"),
		wish.WithHostKeyPath(".ssh/minik"),
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			bm.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				return big{}, opts
			}),
		),
	)
	s.ServerConfigCallback = func(ssh.Context) *gossh.ServerConfig {
		return &gossh.ServerConfig{NoClientAuth: true}
	}
	_ = s.ListenAndServe()
}
