// Package smoketest is the thin framework shared by the stored smoke
// tests in this folder. A "smoke" here is a Go test that drives a LIVE
// running server (SSH-smokes stay expect-based; these are API-level:
// they target the tool endpoints and read back world state).
//
// Run against the container/domain:
//
//	GAME_SMOKE_URL=http://127.0.0.1:8081 go test ./tests/smokes -count=1
//
// Without GAME_SMOKE_URL the suite runs against a STANDALONE in-process
// stack (game + httpapi, memory accounts). Set GAME_SMOKE_LIVE_ONLY=1
// to forbid that (CI-style: only deploy-deployed servers).
package smoketest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/internal/httpapi"
)

// Env hands each smoke a prebuilt client against a live server.
type Env struct {
	t      *testing.T
	base   string
	client *http.Client
}

// NewEnv builds a runner against a live base URL. Calls t.Skip when
// no server is configured, so `go test ./...` stays green without a
// deployed stack.
// New picks the target: a live deployed server when GAME_SMOKE_URL is
// set (the deployed-contract mode), or a standalone in-process stack
// otherwise. Set GAME_SMOKE_LIVE_ONLY=1 to forbid the standalone path
// (CI-style: never run smokes against a virtual world).
func New(t *testing.T) Env {
	t.Helper()
	base := os.Getenv("GAME_SMOKE_URL")
	if base == "" {
		if os.Getenv("GAME_SMOKE_LIVE_ONLY") != "" {
			t.Skip("GAME_SMOKE_URL unset and live-only mode requested")
		}
		return SpinStandalone(t)
	}
	if err := ping(base); err != nil {
		t.Skipf("live server not reachable at %s: %v", base, err)
	}
	return Env{t: t, base: base, client: &http.Client{Timeout: 10 * time.Second}}
}

// ping waits briefly for /healthz (the smoke may race a fresh deploy).
func ping(base string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz %d", res.StatusCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-tick.C:
		}
	}
}

// Do performs one request and DECODES the JSON reply into out (out may
// be nil). Fails the test on non-2xx or bad JSON.
func (e Env) Do(method, path string, body any, out any) int {
	got := e.raw(method, path, body, out)
	if got >= 300 {
		e.t.Fatalf("%s %s -> %d (body above)", method, path, got)
	}
	return got
}

// DoStatus performs one request and asserts a specific status (used
// when a 4xx reply is the expected contract).
func (e Env) DoStatus(method, path string, body any, out any, wantStatus int) int {
	got := e.raw(method, path, body, out)
	if got != wantStatus {
		e.t.Fatalf("%s %s → %d, expected %d", method, path, got, wantStatus)
	}
	return got
}

func (e Env) raw(method, path string, body any, out any) int {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.base+path, rd)
	if err != nil {
		e.t.Fatalf("request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	if out != nil {
		dec := json.NewDecoder(res.Body)
		if err := dec.Decode(out); err != nil {
			e.t.Fatalf("%s %s -> %d: bad json: %v", method, path, res.StatusCode, err)
		}
	}
	return res.StatusCode
}

// Errorf is a direct alias so smokes read like normal tests.
func (e Env) Errorf(format string, args ...any) { e.t.Errorf(format, args...) }

// Log streams into the same logger the game server uses.
func (e Env) Log(format string, args ...any) {
	slog.Info("SLOW-SMOKE " + fmt.Sprintf(format, args...))
}

// SpinStandalone builds an Env around an in-process server stack:
// game.Server + httpapi (tool routes), no databases (memory accounts).
// It's the local fallback so the suite can run without Docker.
func SpinStandalone(t *testing.T) Env {
	t.Helper()
	gs := game.NewServer()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("standalone listener: %v", err)
	}
	srv := &http.Server{Handler: httpapi.NewRouter(gs), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	return Env{t: t, base: "http://" + ln.Addr().String(), client: &http.Client{Timeout: 10 * time.Second}}
}
