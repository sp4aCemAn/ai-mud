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
	ID             int64 // worlds row (tool writes land here)
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
	spawnWorldBase(s, w.Seed, w.WW, w.WH)
	s.objects = append(s.objects, w.Objects...)
	s.replayContent()
	s.landSpawnRing()
	slog.Info("persisted world loaded", "name", w.Name, "seed", w.Seed,
		"size", fmt.Sprintf("%dx%d", w.WW, w.WH), "objects", len(w.Objects))
	return s
}

// spawnWorldBase generates the terrain for a world without autoplay
// content: dots come from either replayContent (persisted worlds) or
// populateDefaultWorld (the classic seed-env flow). The base board is
// the first slice of the infinite chunk plane (same generator the
// absorb path uses — seams never break).
func spawnWorldBase(s *Server, seed int64, ww, wh int) {
	r := rand.New(rand.NewSource(seed))
	w := &worldState{
		ww:        ww,
		wh:        wh,
		wx0:       0,
		wy0:       0,
		seed:      seed,
		chunks:    make(map[chunkKey][]string),
		spawnX:    0,
		spawnY:    0,
		tiles:     genBoard(nil, seed, ww, wh),
		region:    nil,
		enemies:   make(map[int]*Enemy),
		fights:    make(map[string]*Fight),
		shops:     make(map[string]bool),
		rnd:       r,
		maxGroups: 4,
	}
	s.state = w

	// spawn: keep the walkable region's centroid dot (pickSpawn logic
	// over the same glyphs the finite board used; region local==abs —
	// the base board is anchored at 0,0)
	if cells := mainRegion(w.tiles); len(cells) > 0 {
		cx, cy := 0, 0
		for _, c := range cells {
			cx += c[0]
			cy += c[1]
		}
		cx, cy = cx/len(cells), cy/len(cells)
		sx, sy, bestd := cells[0][0], cells[0][1], 1<<30
		for _, c := range cells {
			if d := abs(c[0]-cx) + abs(c[1]-cy); d < bestd {
				sx, sy, bestd = c[0], c[1], d
			}
		}
		w.spawnX, w.spawnY = sx, sy
	}
	w.region = mainRegion(w.tiles)
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

	if base > w.version {
		w.version = base
	}

	// terrain is the infinite chunk plane (fixed by the seed) — no tile
	// regen happens anymore; the record's dims were the BOOT rect.
	w.enemies = map[int]*Enemy{}
	w.npcDot = Dot{X: -1, Y: -1}
	// the record's spawn only wins when it's still walkable on the
	// chunk plane (the tile look changed when the plane replaced the
	// finite generator — a stale spawn coordinate may be water now)
	if spec.SpawnX > 0 || spec.SpawnY > 0 {
		if w.walkableAt(spec.SpawnX, spec.SpawnY) {
			w.spawnX, w.spawnY = spec.SpawnX, spec.SpawnY
		} else {
			sx, sy := pickSpawn(w.tiles, w.ww, w.wh)
			w.spawnX, w.spawnY = sx, sy
			slog.Warn("persisted spawn lands in water — using the region centroid",
				"spawn", fmt.Sprintf("%d,%d", w.spawnX, w.spawnY))
		}
	}

	// first pass: terrain edits (they can open or close tiles)
	for _, o := range s.objects {
		if o.Kind == storage.ObjectEdit {
			s.applyTerrainEdit(o)
		}
	}
	w.changed()

	// second pass: dots, walkable-projected around their anchors
	npcPlaced := false
	for _, o := range s.objects {
		if o.Kind == storage.ObjectEdit {
			continue
		}
		x, y := project(w, o)
		switch o.Kind {
		case storage.ObjectEnemyGroup:
			count, level := groupStats(o)
			if e, ok := w.spawnEnemyAt(o.Name, count, level, x, y); ok {
				e.ObjID = o.ID
			}
			w.changed()
		case storage.ObjectMerchant, storage.ObjectVillage, storage.ObjectNPCGroup:
			// one merchant dot is serviced by the shop engine; the
			// first friendly placement takes the keeper position and
			// later friendly rows await their own shop identities.
			if !npcPlaced {
				w.npcDot = Dot{X: x, Y: y, Kind: "npc", Count: 1, Name: o.Name, ObjID: o.ID}
				npcPlaced = true
				w.changed()
			}
		}
	}
}

// project lands an object's anchor tile on the live terrain: when the
// authored anchor is water (or outside the served rect), spiral-probe
// a walkable tile around it.
func project(w *worldState, o storage.WorldObject) (int, int) {
	x, y := o.HomeX, o.HomeY
	if x >= 0 && y >= 0 && w.walkableAt(x, y) {
		return x, y
	}
	return w.pickRegionSpot(w.spawnX, w.spawnY, 3)
}

// applyTerrainEdit writes one authored terrain tweak (kind=edit rows
// carry glyph patches like [{"x":y,"y":n,"g":"T"}]) into the grid,
// origin-aware because the infinite plane may be serving a rect that
// doesn't start at 0,0. Deltas far outside the rect are skipped.
func (s *Server) applyTerrainEdit(o storage.WorldObject) {
	var deltas []struct {
		X int    `json:"x"`
		Y int    `json:"y"`
		G string `json:"g"`
	}
	if len(o.Tiles) == 0 || json.Unmarshal(o.Tiles, &deltas) != nil {
		return // malformed edit: skip it, keep the world loadable
	}
	w := s.state
	for _, d := range deltas {
		if d.Y < 0 || d.X < 0 {
			continue
		}
		lx, ly := d.X-w.wx0, d.Y-w.wy0
		if ly < 0 || ly >= len(w.tiles) || lx < 0 || lx >= len([]rune(w.tiles[ly])) {
			continue
		}
		glyph := []rune(d.G)
		if len(glyph) == 0 {
			continue
		}
		row := []rune(w.tiles[ly])
		row[lx] = glyph[0]
		w.tiles[ly] = string(row)
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
