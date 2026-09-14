package game

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Towns are nested coordinate spaces: one authored `village` row
// materializes as a deterministic, fixed-size grid. The row is the
// authoritative spec — nothing here opens SQL or persists runtime tile
// mutations (the grid rebuilds identically from the row at every boot).
// TownState is shared read-mostly across every player in it;
// per-player state (convos, stays) lives on Player (slice 3/4).

// townDims clamps the authored radius into a fixed town rect: never
// resized, never regenerated — a town is a town.
func townDims(o storage.WorldObject) (townW, townH int) {
	tw := o.Radius*2 + 5
	if tw < 11 {
		tw = 11
	}
	if tw > 49 {
		tw = 49
	}
	th := 7
	if o.Radius > 4 {
		th = o.Radius + 3
	}
	if th > 19 {
		th = 19
	}
	return tw, th
}

// TownState is one loaded town: a fixed grid with NPC dots.
type TownState struct {
	ID     int64 // the village's world_objects row id
	Name   string
	TW, TH int
	Tiles  []string
	Dots   []Dot // NPC roster (shared; never roams)

	// NPCConvo: authored per-NPC talk lines (keyed by the npc NAME;
	// populated from the row's `convo` entries)
	NPCConvo map[string][]TalkLine

	EntryX, EntryY int // inside the door (where you materialize)
	ExitX, ExitY   int // the doorway tile: stepping back through it exits
}

// townWalkable is the town's own walk check (walls + water block).
func townWalkable(t *TownState, x, y int) bool {
	if x < 0 || y < 0 || y >= len(t.Tiles) || x >= len([]rune(t.Tiles[y])) {
		return false
	}
	return []rune(t.Tiles[y])[x] != tileWater && []rune(t.Tiles[y])[x] != '╬'
}

// buildTown turns an authored village row into the deterministic grid:
// walled rect, one gate doorway on the west rim, authored interior
// patches applied over the base plate, NPC dots projected walkable.
func buildTown(o storage.WorldObject) *TownState {
	tw, th := townDims(o)
	t := &TownState{ID: o.ID, Name: o.Name, TW: tw, TH: th,
		Tiles: make([]string, th)}

	// base plate: walkable floor, fenced by walls on the rim
	for y := 0; y < th; y++ {
		b := []rune(strings.Repeat(",", tw))
		if y == 0 || y == th-1 {
			b = []rune(strings.Repeat("╬", tw))
		} else {
			b[0], b[tw-1] = '╬', '╬'
		}
		t.Tiles[y] = string(b)
	}

	// the west rim's mid tile is the DOOR back out; Entry sits one east
	t.ExitX, t.ExitY = 0, th/2
	t.EntryX, t.EntryY = 1, th/2
	// paint the doorway as a gap in the rim (visual '=' door face)
	col := []rune(t.Tiles[t.ExitY])
	col[t.ExitX] = '='
	t.Tiles[t.ExitY] = string(col)

	// authored interior patches — same glyph-switch edits as the world
	var deltas []struct {
		X int    `json:"x"`
		Y int    `json:"y"`
		G string `json:"g"`
	}

	_ = json.Unmarshal(o.Tiles, &deltas)
	for _, d := range deltas {
		if d.X < 1 || d.X >= tw-1 || d.Y < 1 || d.Y >= th-1 {
			continue // interior patches only: the rim stays a fence
		}
		g := []rune(d.G)
		if len(g) == 0 {
			continue
		}
		col := []rune(t.Tiles[d.Y])
		col[d.X] = g[0]
		t.Tiles[d.Y] = string(col)
	}

	// NPC roster from the payload:
	// {"npcs":[{"type":"innkeep","name":"...","x":n,"y":n,
	//           "convo":[{"text":"...","quest":{...}}]}]}
	var payload struct {
		NPCs []struct {
			Type  string     `json:"type"`
			Name  string     `json:"name"`
			X     int        `json:"x"`
			Y     int        `json:"y"`
			Convo []TalkLine `json:"convo"`
		} `json:"npcs"`
	}
	_ = json.Unmarshal(o.Payload, &payload)
	t.NPCConvo = make(map[string][]TalkLine)
	for _, npc := range payload.NPCs {
		name := npc.Name
		if name == "" {
			name = npc.Type
		}
		x, y := npc.X, npc.Y
		if !townWalkable(t, x, y) {
			x, y = townFreeSpot(t, npc.X, npc.Y)
		}
		t.Dots = append(t.Dots, Dot{X: x, Y: y, Kind: "npc_town",
			Role: npc.Type, Name: name, Count: 1})
		if len(npc.Convo) > 0 {
			t.NPCConvo[name] = npc.Convo
		}
	}

	// guarantee the doors: entry + the tiles east of the exit stay land
	forceTownLand(t, t.EntryX, t.EntryY)
	forceTownLand(t, t.EntryX+1, t.EntryY)
	forceTownLand(t, t.ExitX+1, t.ExitY)
	forceTownLand(t, t.ExitX+2, t.ExitY)

	slog.Info("town built", "id", t.ID, "name", t.Name,
		"size", fmt.Sprintf("%dx%d", t.TW, t.TH), "npcs", len(t.Dots))
	return t
}

// townFreeSpot probes a spiral around an anchor for a walkable, dot-free tile.
func townFreeSpot(t *TownState, sx, sy int) (int, int) {
	for r := 0; r < t.TW+t.TH; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx != r && dx != -r && dy != r && dy != -r {
					continue // spiral rim only
				}
				x, y := sx+dx, sy+dy
				// keep off the doorway (it's traffic, not furniture)
				if (x == t.ExitX || x == t.ExitX+1) && y == t.ExitY {
					continue
				}
				if townWalkable(t, x, y) {
					occupied := false
					for _, d := range t.Dots {
						if d.X == x && d.Y == y {
							occupied = true
							break
						}
					}
					if !occupied {
						return x, y
					}
				}
			}
		}
	}
	return t.EntryX + 2, t.EntryY // degenerate: next to the door
}

// forceTownLand overwrites water with field (the door-ramp guarantee).
func forceTownLand(t *TownState, x, y int) {
	if x < 0 || y < 0 || y >= len(t.Tiles) || x >= len([]rune(t.Tiles[y])) {
		return
	}
	if col := []rune(t.Tiles[y]); col[x] == tileWater {
		col[x] = tileField
		t.Tiles[y] = string(col)
	}
}

// townOrBuild resolves a loaded TownState, building it lazily from its
// authored row (the villages registry is seeded at boot replay).
func (s *Server) townOrBuild(townID int64) *TownState {
	if t, ok := s.towns[townID]; ok {
		return t
	}
	row, ok := s.townRows[townID]
	if !ok {
		return nil
	}
	t := buildTown(row)
	if s.towns == nil {
		s.towns = make(map[int64]*TownState)
	}
	s.towns[townID] = t
	return t
}

// registerTown seeds (or refreshes) the villages registry with an
// authored row — used by the boot replay and by the runtime tool
// surface, so every village row is equally enterable.
func (s *Server) registerTown(row storage.WorldObject) {
	if s.townRows == nil {
		s.townRows = make(map[int64]storage.WorldObject)
	}
	s.townRows[row.ID] = row
	delete(s.towns, row.ID) // a warm state rebuilds from the fresh row
}

// paintGate stamps the village's own 町 onto the world grid at the
// row's home tile — the door mark is intrinsic to the village row (it
// re-lands at every boot replay too, so the doorway never dissolves).
func (s *Server) paintGate(row storage.WorldObject) {
	w := s.state
	s.absorbInto(row.HomeX, row.HomeY, 0, 0)
	lx, ly := row.HomeX-w.wx0, row.HomeY-w.wy0
	if ly < 0 || ly >= len(w.tiles) || lx < 0 || lx >= len([]rune(w.tiles[ly])) {
		return
	}
	if col := []rune(w.tiles[ly]); col[lx] != tileGate {
		col[lx] = tileGate
		w.tiles[ly] = string(col)
		w.changed()
	}
}

// townAt resolves which town claims the absolute world tile (only
// village rows whose anchored gate — the world tile 1 tile offset from
// home — matches; a bare 町 stays paint).
func (s *Server) townAt(x, y int) int64 {
	for _, o := range s.townRows {
		if o.HomeX == x && o.HomeY == y {
			return o.ID
		}
	}
	return 0
}

// enterTown: the 町 step materializes the player inside the town's
// doorway; the world tile they left from becomes the return anchor.
func (s *Server) enterTown(p *Player, townID int64) {
	t := s.townOrBuild(townID)
	if t == nil {
		return // the row vanished mid-step: stay put
	}
	p.townRef = &townRef{TownID: townID, ReturnX: p.X, ReturnY: p.Y}
	p.X, p.Y = t.EntryX, t.EntryY
	s.state.setEvent(p.Fingerprint,
		fmt.Sprintf("you enter %s — the town folds itself around you", t.Name))
	slog.Info("player entered town", "town", t.Name, "fp", p.Fingerprint)
}

// --- slice 3: talk + narration ------------------------------------------------

// NarrStore is the narration-document layer (the harness's authored
// talk lines in the docdb). Wired generically so any backend (and the
// defer Loaded nil path) fits; *storage.Document satisfies it with a
// small adapter — PutNarrDoc/NarrDocByKey live on *Document.
type NarrStore interface {
	PutNarrDoc(ctx context.Context, key string, v any) error
	NarrDocByKey(ctx context.Context, key string, v any) error
}

// DocumentNarrStore adapts the *storage.Document into NarrStore
// (interface-conformance sugar: the methods exist with the same
// shapes, so this keeps a single point of truth for the wiring).
func AttachDocumentNarr(s *Server, d interface {
	PutNarrDoc(ctx context.Context, key string, v any) error
	NarrDocByKey(ctx context.Context, key string, v any) error
}) {
	s.AttachNarrStore(d)
}

// AttachNarrStore wires the conversation read path; nil = the payload
// and canned pool only (the docdb layer is optional).
func (s *Server) AttachNarrStore(store NarrStore) {
	s.narr = store
	if s.narr != nil {
		slog.Info("narration store attached (docdb conversations live)")
	}
}

// TalkLine is one line of conversation; the OPENING line of an NPC's
// talk may lead with a quest spec (the quest grant lands on close).
type TalkLine struct {
	Text  string     `json:"text"`
	Quest *QuestSpec `json:"quest,omitempty"`
}

// QuestSpec rides a talk's final line: a bounty the giver offers.
type QuestSpec struct {
	Title       string `json:"title"`
	Objective   string `json:"objective"` // "kill" for now
	Target      string `json:"target"`    // the enemy group name
	Count       int    `json:"count"`     // how many kills
	RewardCoins int    `json:"reward_coins"`
	RewardXP    int    `json:"reward_xp"`
}

// Talk is the UI-facing conversation state (mirrors Fight/Shop).
type Talk struct {
	TownName string     `json:"town"`
	Name     string     `json:"npc"`
	Role     string     `json:"role"`
	Lines    []TalkLine `json:"lines"`
	Line     int        `json:"-"`
}

// narrKey is the docdb namespace: narr:<world>:<town>:<npc>.
func narrKey(worldID, townID int64, npc string) string {
	return fmt.Sprintf("narr:%d:%d:%s", worldID, townID, npc)
}

// inTownInteract: the movement inside a nested town. Same bump
// semantics as the world, plus slice 3: the NPC bump OPENS A TALK.
func (s *Server) inTownInteract(p *Player, dx, dy int) *Player {
	r := p.townRef
	t := s.townOrBuild(r.TownID)
	w := s.state
	nx, ny := p.X+dx, p.Y+dy
	// the doorway check comes FIRST: the door tile sits on the rim
	if nx == t.ExitX && ny == t.ExitY {
		p.X, p.Y = r.ReturnX, r.ReturnY
		p.townRef = nil
		w.setEvent(p.Fingerprint, fmt.Sprintf("the gate swings you back out of %s", t.Name))
		return p
	}
	if !townWalkable(t, nx, ny) {
		w.setEvent(p.Fingerprint, "the house wall blocks your way")
		return p
	}
	// NPC bump = open the talk session (the UI's overlay mirrors it)
	for i := range t.Dots {
		if t.Dots[i].X == nx && t.Dots[i].Y == ny {
			s.openTalk(p, t, i)
			return p
		}
	}
	if other := s.townOccupiedByPlayer(t, p.Fingerprint, nx, ny); other != "" {
		w.setEvent(p.Fingerprint, fmt.Sprintf("%s stands in your way — talk later", other))
		return p
	}
	p.X, p.Y = nx, ny
	p.lastSeen = time.Now()
	return p
}

// townOccupiedByPlayer finds another co-present player on a town tile.
func (s *Server) townOccupiedByPlayer(t *TownState, fp string, x, y int) string {
	for id, other := range s.players {
		if id == fp || other.townRef == nil || other.townRef.TownID != t.ID {
			continue
		}
		if other.X == x && other.Y == y {
			return other.Name
		}
	}
	return ""
}

// TownView builds the town-rect snapshot (same World contract as the
// outside view — the UI renders it with the same machinery; towns are
// small and fixed, so the camera just centers it, co-presence included).
func (s *Server) TownView(fp string, townID int64) World {
	t := s.townOrBuild(townID)
	if t == nil {
		return s.WorldView(fp) // town row vanished: fall back outside
	}
	out := World{
		W: t.TW, H: t.TH,
		OriginX: 0, OriginY: 0,
		Version: s.townVersion(t),
		Tiles:   append([]string{}, t.Tiles...),
		SpawnX:  t.EntryX, SpawnY: t.EntryY,
	}
	dots := outDots(s, fp, t)
	out.Dots = append(dots, t.Dots...)
	return out
}

// outDots renders the co-presence dots: every OTHER player in this
// town shows up as their own marker (the shared-room effect).
func outDots(s *Server, fp string, t *TownState) []Dot {
	var others []Dot
	for id, other := range s.players {
		if id == fp || other.townRef == nil || other.townRef.TownID != t.ID {
			continue
		}
		others = append(others, Dot{X: other.X, Y: other.Y,
			Kind: "player_town", Count: 1, Name: other.Name})
	}
	return others
}

// townVersion folds the co-presence count into a stable version base —
// town tiles never change in this slice; the roster re-count nudges
// the version so player dots refresh when someone joins/leaves.
func (s *Server) townVersion(t *TownState) uint64 {
	base := uint64(t.TW)*1_000_000 + uint64(t.TH)*1_000
	n := 0
	for id := range s.players {
		if p := s.players[id]; p.townRef != nil && p.townRef.TownID == t.ID {
			n++
		}
	}
	return base + uint64(n)
}
