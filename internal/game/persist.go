package game

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// WorldSpec is one loaded persisted world: identity + authored content.
// The game never opens SQL itself — main.go fetches the active world
// and hands the record here. With no record the server falls back to
// the seed-env world (NewServer), so nothing in the game depends on a
// database being present.
type WorldSpec struct {
	Name           string
	Seed           int64
	WW, WH         int
	SpawnX, SpawnY int
	Objects        []storage.WorldObject
}

// NewServerWorld boots the world from a persisted record: deterministic
// terrain from the record's seed, content replayed from world_objects.
func NewServerWorld(w WorldSpec) *Server {
	s := &Server{
		sessions:  make(map[string]Session),
		tickEvery: 500 * time.Millisecond,
		players:   make(map[string]*Player),
		loaded:    &w,
	}
	spawnWorldBase(s, rand.New(rand.NewSource(w.Seed)), w.WW, w.WH)
	s.replayContent()
	slog.Info("persisted world loaded", "name", w.Name, "seed", w.Seed,
		"size", fmt.Sprintf("%dx%d", w.WW, w.WH), "objects", len(w.Objects))
	return s
}

// spawnWorldBase generates the terrain for a world without autoplay
// content: dots come from either replayContent (persisted worlds) or
// populateDefaultWorld (the classic seed-env flow).
func spawnWorldBase(s *Server, r *rand.Rand, ww, wh int) {
	tiles, sx, sy := genTerrain(r, ww, wh)
	w := &worldState{
		ww:        ww,
		wh:        wh,
		spawnX:    sx,
		spawnY:    sy,
		tiles:     tiles,
		region:    mainRegion(tiles),
		enemies:   make(map[int]*Enemy),
		fights:    make(map[string]*Fight),
		shops:     make(map[string]bool),
		rnd:       r,
		maxGroups: 4,
	}
	s.state = w
}

// replayContent re-places authored objects on the CURRENT terrain:
// called at load and after every terrain regen (resize). Authored
// content beats autoplay spawns — the generated sets are dropped and
// replaced, which keeps persisted worlds stable across resizes.
// replayContent is the lock-taking entry point (tool calls and boot).
func (s *Server) replayContent() {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	s.replayContentLocked()
}

// replayContentLocked is the core; the caller holds playersMu (boot,
// regenWorld, and the tool paths use different scopes).
func (s *Server) replayContentLocked() {
	s.replayContentReared(0)
}

// replayContentContinue preserves a base version (regenWorld feeds the
// pre-regen version so the counter never rewinds across terrain swaps).
func (s *Server) replayContentReared(base uint64) {
	spec := s.loaded
	if spec == nil {
		return // not a persisted world
	}
	s.regenCount = 0 // the record's seed is authoritative from here
	w := s.state

	// deterministic terrain from the record (the autoplay seed ladder
	// no longer applies to content)
	if base > w.version {
		w.version = base
	}

	fresh, _, _ := genTerrain(rand.New(rand.NewSource(spec.Seed)), spec.WW, spec.WH)
	w.tiles = fresh
	w.region = mainRegion(w.tiles)
	if spec.SpawnX > 0 || spec.SpawnY > 0 {
		w.spawnX, w.spawnY = spec.SpawnX, spec.SpawnY
	}
	w.enemies = map[int]*Enemy{}
	w.npcDot = Dot{X: -1, Y: -1}

	// first pass: terrain edits (they can open or close tiles)
	var placements []storage.WorldObject
	for _, o := range spec.Objects {
		if o.Kind == storage.ObjectEdit {
			applyTerrainEdit(w, o)
			continue
		}
		placements = append(placements, o)
	}
	w.changed()

	// second pass: dots, walkable-projected around their anchors
	npcPlaced := false
	for _, o := range placements {
		x, y := project(w, o)
		switch o.Kind {
		case storage.ObjectEnemyGroup:
			count, level := groupStats(o)
			w.spawnEnemyAt(o.Name, count, level, x, y)
			w.changed()
		case storage.ObjectMerchant, storage.ObjectVillage, storage.ObjectNPCGroup:
			// one merchant dot is serviced by the shop engine; the
			// first friendly placement takes the keeper position and
			// later friendly rows await their own shop identities.
			if !npcPlaced {
				w.npcDot = Dot{X: x, Y: y, Kind: "npc", Count: 1, Name: o.Name}
				npcPlaced = true
				w.changed()
			}
		}
	}
}

// project lands an object's anchor tile on the live terrain, nudging
// to the nearest region spot when the authored tile is water/outside.
func project(w *worldState, o storage.WorldObject) (int, int) {
	x, y := o.HomeX, o.HomeY
	if x >= 0 && y >= 0 && x < w.ww && y < w.wh && walkable(w.tiles, x, y) {
		return x, y
	}
	return w.pickRegionSpot(w.spawnX, w.spawnY, 3)
}

// applyTerrainEdit writes one authored terrain tweak (kind=edit rows
// carry glyph patches like [{"x":n,"y":n,"g":"T"}], applied post-regen).
func applyTerrainEdit(w *worldState, o storage.WorldObject) {
	var deltas []struct {
		X int    `json:"x"`
		Y int    `json:"y"`
		G string `json:"g"`
	}
	if len(o.Tiles) == 0 || json.Unmarshal(o.Tiles, &deltas) != nil {
		return // malformed edit: skip it, keep the world loadable
	}
	for _, d := range deltas {
		if d.Y < 0 || d.Y >= w.wh || d.X < 0 || d.X >= w.ww {
			continue
		}
		glyph := []rune(d.G)
		if len(glyph) == 0 {
			continue
		}
		row := []rune(w.tiles[d.Y])
		row[d.X] = glyph[0]
		w.tiles[d.Y] = string(row)
	}
	w.changed()
}

// groupStats pulls count/level out of an enemy_group payload ("data"
// jsonb: {"count":2,"level":1}) with benign defaults, clamped.
func groupStats(o storage.WorldObject) (int, int) {
	var d struct {
		Count int `json:"count"`
		Level int `json:"level"`
	}
	_ = json.Unmarshal(o.Payload, &d)
	return clamp(d.Count, 1, 12), clamp(d.Level, 1, 99)
}
