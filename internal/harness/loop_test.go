package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sp4aceman/ai-mud/internal/game"
)

// The loop's unit pins: observe → prompt → tolerant parse → verbs. The
// LLM endpoint is REAL HTTP but scripted here; the mock server in
// tests/harness_mock rides the same contract in CI.

// fakeGame captures what the loop applied.
type fakeGame struct {
	mu        sync.Mutex
	announces []string
	events    []game.GMEvent
}

func (g *fakeGame) Observe() game.GMReadout {
	g.mu.Lock()
	ev := g.events
	g.events = nil
	g.mu.Unlock()
	return game.GMReadout{
		World:  game.WorldSummary{W: 40, H: 12, PlayerCount: 2, EnemyCount: 3},
		Towns:  []game.TownCard{{ID: 7, Name: "ashfen", Door: [2]int{30, 12}, Roles: []string{"innkeep"}}},
		Events: ev,
	}
}

func (g *fakeGame) GMAnnounce(text string) error {
	if strings.TrimSpace(text) == "" {
		return errorsNew("empty announcement")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.announces = append(g.announces, text)
	return nil
}

func (g *fakeGame) Announced() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string{}, g.announces...)
}

func errorsNew(s string) error { return &simpleError{s} }

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// scriptedLLM serves replies FIFO over REAL HTTP so the loop runs
// fully wired through the same client the live server uses.
func scriptedLLM(t *testing.T, replies ...string) (baseURL string) {
	t.Helper()
	mu := &sync.Mutex{}
	idx := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"mock"}]}`))
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reply := "the world holds its breath" // the mock contract's no-op line
		if idx < len(replies) {
			reply = replies[idx]
		}
		idx++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": reply}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// TestLoopParsesAndAnnounces: an idle world before any action skips its
// cycles; one login event earns a prompt; the model's JSON announces.
func TestLoopParsesAndAnnounces(t *testing.T) {
	base := scriptedLLM(t,
		`{"actions":[{"type":"announce","text":"a cold wind tonight"}]}`,
	)
	cfg := Config{Enabled: true, BaseURL: base, Model: "mock",
		Temperature: 0.3, MaxTokens: 64, Cadence: "40ms"}
	g := &fakeGame{}
	h := New(cfg, g)

	g.mu.Lock()
	g.events = []game.GMEvent{{Kind: "login", Text: "Rae enters the world"}}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := h.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	ann := g.Announced()
	if len(ann) != 1 || ann[0] != "a cold wind tonight" {
		t.Fatalf("announces: %+v", ann)
	}
}

// TestLoopIgnoresProseNoise: the mock's default line (bare prose, no
// JSON) must stay a NO-OP — the model's chit-chat never floods player
// event lines with an idle rambling.
func TestLoopIgnoresProseNoise(t *testing.T) {
	base := scriptedLLM(t)
	cfg := Config{Enabled: true, BaseURL: base, Model: "mock",
		Temperature: 0.3, MaxTokens: 64, Cadence: "40ms"}
	g := &fakeGame{}
	h := New(cfg, g)

	g.mu.Lock()
	g.events = []game.GMEvent{{Kind: "login", Text: "Rae enters the world"}}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = h.Run(ctx)

	if ann := g.Announced(); len(ann) != 0 {
		t.Fatalf("prose must stay a no-op: %+v", ann)
	}
}

// TestParseActionsTolerant: the parse layers — clean JSON, fenced JSON,
// prose around JSON, a bare action object — and total noise (nil).
func TestParseActionsTolerant(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		want  int
		first string
	}{
		{"clean", `{"actions":[{"type":"announce","text":"hi"}]}`, 1, "hi"},
		{"fenced", "```json\n{\"actions\":[{\"type\":\"announce\",\"text\":\"wolves\"}]}\n```", 1, "wolves"},
		{"prosewrapped", "Of course! Here is my plan:\n{\"actions\":[{\"type\":\"announce\",\"text\":\"the tide turns\"}]}", 1, "the tide turns"},
		{"bareobject", `{"type":"announce","text":"one shout"}`, 1, ""},
		{"noise", "the world holds its breath", 0, ""},
		{"empty", `{"actions":[]}`, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseActions(tc.text)
			if len(got) != tc.want {
				t.Fatalf("actions count: %+v", got)
			}
			if tc.first != "" && got[0].Text != tc.first {
				t.Fatalf("first action text: %+v", got)
			}
		})
	}
}
