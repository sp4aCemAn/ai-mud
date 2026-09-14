package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Part 3 tool surface: place/edit/remove verbs wired to world_objects.
// The game never opens SQL — the AI game master calls these verbs,
// each of which writes the authored row through WorldStore AND applies
// the change to the live world. With no store attached (memory mode,
// unit tests) the verbs stay in-memory only.

// WorldStore is the persistence half the tools need. *storage.Relational
// satisfies it; a fake covers the tests.
type WorldStore interface {
	UpsertObject(ctx context.Context, o *storage.WorldObject) error
	DeleteObject(ctx context.Context, objectID int64) error
}

// AttachWorldStore wires tool persistence: worldID is the persisted
// worlds row the live world was loaded from (0 = seed-env flow, where
// tool calls stay live-only).
func (s *Server) AttachWorldStore(store WorldStore, worldID int64) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	s.wstore = store
	if s.loaded != nil {
		s.loaded.ID = worldID
	}
	slog.Info("tool persistence attached", "worldID", worldID)
}

// ObjectSpec is one tool-authored placement.
type ObjectSpec struct {
	Kind   string          `json:"kind"` // village | faction | npc_group | enemy_group | merchant | edit
	Name   string          `json:"name"`
	X      *int            `json:"x,omitempty"` // anchor; nil = a region spot (or n/a for edits)
	Y      *int            `json:"y,omitempty"`
	Radius int             `json:"radius,omitempty"` // footprint hint, stored for later parts
	Tiles  json.RawMessage `json:"tiles,omitempty"`  // building/road/zone glyphs (and edit deltas)
	Data   json.RawMessage `json:"data,omitempty"`   // kind payload
}

// ObjectSummary is the placement readback: what landed, where.
type ObjectSummary struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Radius int    `json:"radius"`
}

// ObjectPatch is an update_object body: nil fields stay as authored.
type ObjectPatch struct {
	Name   *string          `json:"name,omitempty"`
	Radius *int             `json:"radius,omitempty"`
	Tiles  *json.RawMessage `json:"tiles,omitempty"`
	Data   *json.RawMessage `json:"data,omitempty"`
}

// TerrainEditSpec patches walkable glyphs post-regen (kind=edit row).
type TerrainEditSpec struct {
	Name   string // label, e.g. "road south"
	Tiles  []TileEdit
	Radius int
}

// TileEdit writes one glyph at one tile.
type TileEdit struct {
	X int    `json:"x"`
	Y int    `json:"y"`
	G string `json:"g"`
}

// PlaceObject authors one placement: the world_objects row is written
// first (the source of truth), then the live world reflects it. Anchor
// is honored when in-bounds and walkable, otherwise a region spot wins.
func (s *Server) PlaceObject(spec ObjectSpec) (ObjectSummary, error) {
	if !storage.KnownObjectKind(spec.Kind) {
		return ObjectSummary{}, fmt.Errorf("unknown kind %q", spec.Kind)
	}
	if strings.TrimSpace(spec.Name) == "" && spec.Kind != storage.ObjectEdit {
		return ObjectSummary{}, errors.New("name required")
	}
	if spec.Kind == storage.ObjectEdit && len(spec.Tiles) == 0 {
		return ObjectSummary{}, errors.New("edit needs tiles ({x,y,g} deltas)")
	}

	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state

	x, y := 0, 0
	if spec.Kind == storage.ObjectEdit {
		// edits carry their own tile deltas; the anchor is nominal
	} else if spec.X == nil || spec.Y == nil {
		x, y = w.pickRegionSpot(w.spawnX, w.spawnY, 3)
	} else {
		if *spec.X < 0 || *spec.Y < 0 || *spec.X >= w.ww || *spec.Y >= w.wh {
			return ObjectSummary{}, errors.New("anchor tile outside the world")
		}
		if !walkable(w.tiles, *spec.X, *spec.Y) {
			return ObjectSummary{}, errors.New("anchor tile is not walkable")
		}
		x, y = *spec.X, *spec.Y
	}

	row := toObjectRow(spec, s.worldID())
	row.HomeX, row.HomeY = x, y // the row records where the dot really is
	if s.wstore != nil {
		if err := s.wstore.UpsertObject(context.Background(), &row); err != nil {
			return ObjectSummary{}, fmt.Errorf("persistence rejected the placement: %w", err)
		}
	} else {
		s.nextEphemeral-- // memory mode: still addressable (negative ids)
		row.ID = s.nextEphemeral
	}
	s.objects = append(s.objects, row)
	s.applyObject(row, x, y)

	summary := ObjectSummary{ID: row.ID, Kind: row.Kind, Name: row.Name, X: x, Y: y, Radius: row.Radius}
	slog.Info("object placed", "kind", row.Kind, "name", row.Name,
		"at", fmt.Sprintf("%d,%d", x, y), "id", row.ID, "source", "tool")
	return summary, nil
}

// TerrainEdit is the edit_terrain verb: deltas land as a kind=edit row
// and the glyphs are applied to the live tiles immediately.
func (s *Server) TerrainEdit(spec TerrainEditSpec) (ObjectSummary, error) {
	raw, err := json.Marshal(spec.Tiles)
	if err != nil {
		return ObjectSummary{}, err
	}
	return s.PlaceObject(ObjectSpec{
		Kind:   storage.ObjectEdit,
		Name:   spec.Name,
		Radius: spec.Radius,
		Tiles:  raw,
	})
}

// UpdateObject reshapes one authored row (edit-by-id); the live world
// then reflects it either through a record replay (persisted worlds)
// or by a direct re-apply (memory mode).
func (s *Server) UpdateObject(id int64, patch ObjectPatch) (ObjectSummary, error) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	idx := s.indexOf(id)
	if idx < 0 {
		return ObjectSummary{}, errors.New("no such authored object")
	}
	row := s.objects[idx]
	oldRow := row
	if patch.Name != nil {
		row.Name = *patch.Name
	}
	if patch.Radius != nil {
		row.Radius = *patch.Radius
	}
	if patch.Tiles != nil {
		row.Tiles = *patch.Tiles
	}
	if patch.Data != nil {
		row.Payload = *patch.Data
	}

	if s.wstore != nil {
		if err := s.wstore.UpsertObject(context.Background(), &row); err != nil {
			return ObjectSummary{}, fmt.Errorf("persistence rejected the edit: %w", err)
		}
	}
	s.objects[idx] = row

	if s.loaded != nil {
		// replay from the record: deterministic terrain, fresh content
		s.replayContentLocked()
	} else {
		// memory mode: drop the old live form, re-apply the row
		s.dropLive(oldRow.ID)
		x, y := project(s.state, row)
		s.applyObject(row, x, y)
	}
	out := ObjectSummary{ID: row.ID, Kind: row.Kind, Name: row.Name,
		X: row.HomeX, Y: row.HomeY, Radius: row.Radius}
	slog.Info("object updated", "kind", row.Kind, "name", row.Name, "id", row.ID, "source", "tool")
	return out, nil
}

// RemoveObject deletes one authored placement and drops its live form.
// Despawn-style reconciliation: no double-free when re-run (a second
// call reports not-found).
func (s *Server) RemoveObject(id int64) error {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	idx := s.indexOf(id)
	if idx < 0 {
		return errors.New("no such authored object")
	}
	row := s.objects[idx]
	if s.wstore != nil {
		if err := s.wstore.DeleteObject(context.Background(), id); err != nil &&
			!errors.Is(err, storage.ErrNotFound) {
			// an already-gone row is fine (another tool removed it);
			// the live form still drops below — idempotent by design
			return fmt.Errorf("persistence rejected the removal: %w", err)
		}
	}
	s.objects = append(s.objects[:idx], s.objects[idx+1:]...)

	if s.loaded != nil {
		s.replayContentLocked() // live form drops with the row
	} else {
		s.dropLive(row.ID) // memory mode: no replay to lean on
	}
	slog.Info("object removed", "kind", row.Kind, "name", row.Name, "id", id,
		"source", "tool")
	return nil
}

// Objects readback: the authored placements (optionally by kind).
func (s *Server) Objects(kinds ...string) []ObjectSummary {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	out := []ObjectSummary{}
	for _, o := range s.objects {
		if len(want) > 0 && !want[o.Kind] {
			continue
		}
		out = append(out, ObjectSummary{ID: o.ID, Kind: o.Kind, Name: o.Name,
			X: o.HomeX, Y: o.HomeY, Radius: o.Radius})
	}
	return out
}

// --- internals ---------------------------------------------------------------

// toObjectRow shapes a spec into the storage row (home lands at the
// applied anchor so replay finds the dot where it really is).
func toObjectRow(spec ObjectSpec, worldID int64) storage.WorldObject {
	x, y := 0, 0
	if spec.X != nil {
		x, y = *spec.X, *spec.Y
	}
	return storage.WorldObject{
		WorldID: worldID,
		Kind:    spec.Kind,
		Name:    spec.Name,
		Seed:    0,
		HomeX:   x,
		HomeY:   y,
		Radius:  spec.Radius,
		Tiles:   spec.Tiles,
		Payload: spec.Data,
	}
}

// applyObject puts one authored row on the live world (the PlaceObject
// fast path — the same switch replayContentLocked runs after regen).
func (s *Server) applyObject(o storage.WorldObject, x, y int) {
	w := s.state
	switch o.Kind {
	case storage.ObjectEdit:
		s.applyTerrainEdit(o)
	case storage.ObjectEnemyGroup:
		count, level := groupStats(o)
		if e, ok := w.spawnEnemyAt(o.Name, count, level, x, y); ok {
			e.ObjID = o.ID
		}
	case storage.ObjectMerchant, storage.ObjectVillage, storage.ObjectNPCGroup:
		if w.npcDot.X < 0 { // first friendly placement takes the keeper dot
			w.npcDot = Dot{X: x, Y: y, Kind: "npc", Count: 1, Name: o.Name, ObjID: o.ID}
		}
	}
	w.changed()
}

// dropLive removes one placement's live form without a replay
// (memory mode): the enemy dot with a matching row, or the keeper dot.
func (s *Server) dropLive(objID int64) {
	w := s.state
	for id, e := range w.enemies {
		if e.ObjID == objID {
			delete(w.enemies, id)
		}
	}
	if w.npcDot.ObjID == objID {
		w.npcDot = Dot{X: -1, Y: -1, ObjID: 0}
	}
	w.changed()
}

// indexOf finds a tool-placed row by id on the live listing.
func (s *Server) indexOf(id int64) int {
	for i, o := range s.objects {
		if o.ID == id {
			return i
		}
	}
	return -1
}

// worldID resolves the worlds row the tool writes into (0 when none).
func (s *Server) worldID() int64 {
	if s.loaded != nil {
		return s.loaded.ID
	}
	return 0
}
