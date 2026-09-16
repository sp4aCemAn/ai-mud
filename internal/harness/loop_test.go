package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	spawns    []string
	villages  []string
	events    []game.GMEvent
	poke      chan struct{}
}

func (g *fakeGame) Poke() <-chan struct{} { return g.poke }

func (g *fakeGame) Observe() game.GMReadout {
	g.mu.Lock()
	ev := g.events
	g.events = nil
	g.mu.Unlock()
	return game.GMReadout{
		World:   game.WorldSummary{W: 40, H: 12, PlayerCount: 2, EnemyCount: 3},
		Towns:   []game.TownCard{{ID: 7, Name: "ashfen", Door: [2]int{30, 12}, Roles: []string{"innkeep"}}},
		Terrain: game.TerrainCard{Cap: 3, Settled: 2, Frontier: 1},
		Events:  ev,
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

func (g *fakeGame) GMSpawnEnemies(count, level int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawns = append(g.spawns, fmt.Sprintf("%dx%d", count, level))
	return nil
}

func (g *fakeGame) GMRaiseVillage(name string) error {
	if strings.TrimSpace(name) == "" {
		return errorsNew("a village needs a name")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.villages = append(g.villages, name)
	return nil
}

func (g *fakeGame) ApplyLog() ([]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string{}, g.spawns...), append([]string{}, g.villages...)
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
	g := &fakeGame{poke: make(chan struct{}, 1)}
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
	g := &fakeGame{poke: make(chan struct{}, 1)}
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

// TestLoopTwoStageCactus: the full two-stage shape — stage 1 (persona,
// no tools) returns pure UNIVERS prose; stage 2 (interpreter, tools)
// returns native tool_calls and the loop applies the parsed verb.
// The endpoint rejects nothing here: it's the SAME server the live
// cactus `serve` run contracts (standard tool_calls wire).
func TestLoopTwoStageCactus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"mock"}]}`))
			return
		}
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad body: %v", err)
			return
		}
		if len(req.Tools) > 0 {
			// the interpreter turn: a native tool_calls reply (local-only)
			_, _ = w.Write([]byte(toolCallsReply))
			return
		}
		// the persona turn: prose, NO json protocol needed
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant",
			"content":"A cold wind is rising. The player should hear it."}}]}`))
	}))
	t.Cleanup(srv.Close)

	// the verb card file (the same shape configs/gm_tools.json ships)
	dir := t.TempDir()
	path := dir + "/tools.json"
	tools := `{"tools":[{"type":"function","function":{"name":"announce",
		"description":"one line","parameters":{"type":"object",
		"properties":{"text":{"type":"string"}},"required":["text"]}}}]}`
	if err := writeFileStr(path, tools); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Enabled: true, BaseURL: srv.URL + "/v1", Model: "mock",
		Temperature: 0.3, MaxTokens: 64, Cadence: "40ms", ToolsPath: path}
	g := &fakeGame{poke: make(chan struct{}, 1)}
	g.mu.Lock()
	g.events = []game.GMEvent{{Kind: "explore", Text: "fresh country"}}
	g.mu.Unlock()
	h := New(cfg, g)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := h.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	ann := g.Announced()
	if len(ann) != 1 || ann[0] != "a cold wind rises from the moor" {
		t.Fatalf("the interpreter's tool call must become the announce: %+v", ann)
	}
}

func writeFileStr(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// toolCallsReply is what cactus `serve` hands back for a tool turn
// (recorded from the probe; the standard OpenAI wire shape).
const toolCallsReply = `{"id":"chatcmpl-probe","object":"chat.completion",
"model":"gemma-4-e2b-it-cq4",
"choices":[{"index":0,"message":{"role":"assistant","content":null,
"tool_calls":[{"id":"call_09a7","type":"function","function":{"name":"announce",
"arguments":"{\"text\": \"a cold wind rises from the moor\"}"}}]},
"finish_reason":"tool_calls"}],
"cloud_handoff":false}`
