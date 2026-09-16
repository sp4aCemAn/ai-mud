package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sp4aceman/ai-mud/internal/storage"
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
	if s.X != nil && (*s.X < 0 || *s.Y < 0) {
		return errors.New("anchor tile outside the world")
	}
	// tiles beyond the current rect grow in on demand (infinite plane)
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
	} else if !w.walkableAt(*spec.X, *spec.Y) {
		return Enemy{}, errors.New("anchor tile is not walkable")
	} else {
		x, y = *spec.X, *spec.Y
	}
	e, ok := w.spawnEnemyAt(spec.Name, spec.Count, spec.Level, x, y)
	if !ok {
		return Enemy{}, errors.New("no free tile near the anchor")
	}
	// tool spawns land in world_objects too, so resize/restart replays
	// keep them (persistence failure is soft: the group stays live)
	if s.wstore != nil {
		data, _ := json.Marshal(map[string]int{"count": spec.Count, "level": spec.Level})
		row := storage.WorldObject{
			WorldID: s.worldID(), Kind: storage.ObjectEnemyGroup,
			Name: spec.Name, HomeX: x, HomeY: y, Payload: data,
		}
		if err := s.wstore.UpsertObject(context.Background(), &row); err != nil {
			slog.Warn("enemy group persisted live-only", "err", err)
		} else {
			e.ObjID = row.ID
			s.objects = append(s.objects, row)
		}
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
	objID := w.enemies[id].ObjID
	delete(w.enemies, id)
	// the authored row drops with the live group (one authoritative
	// removal — a replay will not resurrect it)
	if objID != 0 && s.wstore != nil {
		if err := s.wstore.DeleteObject(context.Background(), objID); err != nil {
			slog.Warn("enemy row delete failed", "id", objID, "err", err)
		}
		for i, o := range s.objects {
			if o.ID == objID {
				s.objects = append(s.objects[:i], s.objects[i+1:]...)
				break
			}
		}
	}
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
	for fp := range s.players {
		s.state.setEvent(fp, text)
	}
	s.gmNote("announce", text)
	slog.Info("game master announces", "text", text)
}

// WorldSummary is the harness/ops view of the live world: enough to
// verify that a tool call landed, without the full tile dump.
type WorldSummary struct {
	W           int      `json:"w"`
	H           int      `json:"h"`
	OriginX     int      `json:"originX"` // abs coord of the served rect's corner
	OriginY     int      `json:"originY"`
	SpawnX      int      `json:"spawnX"`
	SpawnY      int      `json:"spawnY"`
	Version     uint64   `json:"version"`
	InTown      bool     `json:"inTown"` // will read: who's nested where
	TownID      int64    `json:"townId,omitempty"`
	TownName    string   `json:"townName,omitempty"`
	EnemyCount  int      `json:"enemyCount"`
	EnemyNames  []string `json:"enemyNames"`
	MerchantX   int      `json:"merchantX"`
	MerchantY   int      `json:"merchantY"`
	PlayerCount int      `json:"playerCount"`
}

// TileAt returns one absolute tile's data glyph (operator/debug reads;
// the infinite plane's tiles render through OriginX/OriginY offsets).
func (s *Server) TileAt(x, y int) rune {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	return s.state.tileAt(x, y)
}

// WorldSummary snapshots the current world for tooling and smoke tests.
func (s *Server) WorldSummary(fp string) WorldSummary {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	out := WorldSummary{
		W: w.ww, H: w.wh,
		OriginX: w.wx0, OriginY: w.wy0,
		SpawnX: w.spawnX, SpawnY: w.spawnY,
		Version:   w.version,
		MerchantX: w.npcDot.X, MerchantY: w.npcDot.Y,
		PlayerCount: len(s.players),
	}
	if p := s.players[fp]; p != nil && p.townRef != nil {
		out.InTown = true
		out.TownID = p.townRef.TownID
		if t := s.towns[p.townRef.TownID]; t != nil {
			out.TownName = t.Name
		}
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
