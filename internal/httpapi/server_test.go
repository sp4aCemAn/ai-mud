package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/game"
)

func stringReader(body string) *strings.Reader { return strings.NewReader(body) }

func int64String(n int) string { return fmt.Sprintf("%d", n) }

func testGame() *game.Server {
	gs := game.NewServer()
	gs.DebugFlattenWorld()
	return gs
}

func TestHealthz(t *testing.T) {
	h := NewRouter(testGame())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || string(res.Body.Bytes()) != "ok\n" {
		t.Fatalf("healthz: %d %q", res.Code, res.Body.String())
	}
}

func TestWorldSummaryEndpoint(t *testing.T) {
	h := NewRouter(testGame())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/world", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("/api/world: %d", res.Code)
	}
	var sum game.WorldSummary
	if err := json.Unmarshal(res.Body.Bytes(), &sum); err != nil {
		t.Fatalf("body not json: %v %q", err, res.Body.String())
	}
	if sum.W <= 0 || sum.H <= 0 {
		t.Fatalf("summary dims empty: %+v", sum)
	}
	if sum.Version == 0 {
		t.Fatal("summary version zero")
	}
}

func TestSpawnAndDespawnViaHTTP(t *testing.T) {
	gs := testGame()
	h := NewRouter(gs)

	// spawn with an explicit anchor
	body := `{"name":"squatters","count":2,"level":1,"x":11,"y":11}`
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/world/enemies", stringReader(body)))
	if res.Code != http.StatusCreated {
		t.Fatalf("spawn: %d %q", res.Code, res.Body.String())
	}
	var e game.Enemy
	if err := json.Unmarshal(res.Body.Bytes(), &e); err != nil {
		t.Fatalf("created body: %v", err)
	}
	if e.ID <= 0 || e.X != 11 || e.Y != 11 || e.Name != "squatters" {
		t.Fatalf("created group wrong: %+v", e)
	}

	// readback lists it
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/world/enemies", nil))
	var rows []game.EnemySummary
	if err := json.Unmarshal(res.Body.Bytes(), &rows); err != nil || len(rows) == 0 {
		t.Fatalf("enemies readback: %v %q", err, res.Body.String())
	}

	// bad payload → 400 with an error field
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/world/enemies", stringReader(`{"count":-3}`)))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("bad spawn should be 400, got %d", res.Code)
	}

	// unknown id → 404
	res = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/world/enemies/"+int64String(e.ID+99999), nil)
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown despawn should be 404, got %d", res.Code)
	}

	// despawn the real one → 204
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/api/world/enemies/"+int64String(e.ID), nil))
	if res.Code != http.StatusNoContent {
		t.Fatalf("despawn: %d", res.Code)
	}
	rows = gs.Enemies()
	for _, row := range rows {
		if row.ID == e.ID {
			t.Fatal("group still live after despawn")
		}
	}
}

func TestAnnounceEndpoint(t *testing.T) {
	h := NewRouter(testGame())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/world/announce", stringReader(`{"text":"the sky opens"}`)))
	if res.Code != http.StatusAccepted {
		t.Fatalf("announce: %d %q", res.Code, res.Body.String())
	}
}
