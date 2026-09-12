package game

import (
	"errors"
	"fmt"
	"log/slog"
)

// Spice is the tool-call surface: the harness (AI game master) and any
// operator tool mutate the world only through these verbs, never by
// reaching into the worldState. Every verb bumbs the render version so
// connected players see the change within one renderer frame.

// SpawnEnemyGroupSpec is one authored hostile group.
type SpawnEnemyGroupSpec struct {
	Name  string // display name, e.g. "black choir of ashfen"
	Count int    // 1-4 baddies per dot (clamped)
	Level int    // 1-3 by default bands (clamped 1-99)
	X, Y  *int   // anchor tile; nil = a random region spot (so tile 0 doesn't hide)
}

// Validate clamps and checks the spec against the live world.
func (s SpawnEnemyGroupSpec) valid(w *worldState) error {
	if s.Name == "" {
		return errors.New("name required")
	}
	if s.Count <= 0 || s.Count > 12 {
		return errors.New("count out of range 1..12")
	}
	if s.Level <= 0 || s.Level > 99 {
		return errors.New("level out of range 1..99")
	}
	if (s.X == nil) != (s.Y == nil) {
		return errors.New("both anchor coords required (or neither)")
	}
	if s.X != nil && (*s.X < 0 || *s.Y < 0 || *s.X >= w.ww || *s.Y >= w.wh) {
		return errors.New("anchor tile outside the world")
	}
	// tile must exist (water rejects too — the caller checks walkable)
	return nil
}

// SpawnEnemyGroup places one hostile group dot on the live world.
// Anchor (X, Y) is honored when in-bounds and walkable, otherwise the
// group lands on a random region spot. Returns the created group.
func (s *Server) SpawnEnemyGroup(spec SpawnEnemyGroupSpec) (Enemy, error) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	if err := spec.valid(w); err != nil {
		return Enemy{}, err
	}
	x, y := 0, 0
	if spec.X == nil || spec.Y == nil {
		x, y = w.randomSpot()
	} else if !walkable(w.tiles, *spec.X, *spec.Y) {
		return Enemy{}, errors.New("anchor tile is not walkable")
	} else {
		x, y = *spec.X, *spec.Y
	}
	e, ok := w.spawnEnemyAt(spec.Name, spec.Count, spec.Level, x, y)
	if !ok {
		return Enemy{}, errors.New("no free tile near the anchor")
	}
	w.changed()
	slog.Info("enemy group spawned", "name", e.Name, "at", fmt.Sprintf("%d,%d", e.X, e.Y), "source", "tool")
	return *e, nil
}

// DespawnEnemy removes one enemy group by id. Used by tools and
// (later) by persistence to reconcile authored content.
func (s *Server) DespawnEnemy(id int) bool {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	if _, ok := w.enemies[id]; !ok {
		return false
	}
	name := w.enemies[id].Name
	delete(w.enemies, id)
	w.changed()
	slog.Info("enemy group despawned", "id", id, "name", name, "source", "tool")
	return true
}

// Announce pushes a global line into every player's event log. The AI
// narrates through this; players see it in the world log pane.
func (s *Server) Announce(text string) {
	if text == "" {
		return
	}
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	s.state.setEvent("global", text)
	slog.Info("game master announces", "text", text)
}

// WorldSummary is the harness/ops view of the live world: enough to
// verify that a tool call landed, without the full tile dump.
type WorldSummary struct {
	W           int      `json:"w"`
	H           int      `json:"h"`
	SpawnX      int      `json:"spawnX"`
	SpawnY      int      `json:"spawnY"`
	Version     uint64   `json:"version"`
	EnemyCount  int      `json:"enemyCount"`
	EnemyNames  []string `json:"enemyNames"`
	MerchantX   int      `json:"merchantX"`
	MerchantY   int      `json:"merchantY"`
	PlayerCount int      `json:"playerCount"`
}

// WorldSummary snapshots the current world for tooling and smoke tests.
func (s *Server) WorldSummary() WorldSummary {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	out := WorldSummary{
		W: w.ww, H: w.wh,
		SpawnX: w.spawnX, SpawnY: w.spawnY,
		Version:   w.version,
		MerchantX: w.npcDot.X, MerchantY: w.npcDot.Y,
		PlayerCount: len(s.players),
	}
	out.EnemyNames = make([]string, 0, len(w.enemies))
	for _, e := range w.enemies {
		out.EnemyCount++
		out.EnemyNames = append(out.EnemyNames, e.Name)
	}
	return out
}

// EnemySummary is one group's public shape (tool-call readback).
type EnemySummary struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Count int    `json:"count"`
	Level int    `json:"level"`
	HP    int    `json:"hp"`
	MaxHP int    `json:"maxHP"`
}

// Enemies lists the live groups (tool readback).
func (s *Server) Enemies() []EnemySummary {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	out := make([]EnemySummary, 0, len(w.enemies))
	for _, e := range w.enemies {
		out = append(out, EnemySummary{
			ID: e.ID, Name: e.Name, X: e.X, Y: e.Y,
			Count: e.Count, Level: e.Level, HP: e.HP, MaxHP: e.MaxHP,
		})
	}
	return out
}
