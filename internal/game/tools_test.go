package game

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// fakeStore keeps world_objects rows in memory — the tool verbs must
// treat it exactly like the Relational store: IDs assigned on insert,
// edit-by-id, not-found removals.
type fakeStore struct {
	rows    map[int64]storage.WorldObject
	nextID  int64
	worldID int64
}

func newFakeStore(worldID int64) *fakeStore {
	return &fakeStore{rows: map[int64]storage.WorldObject{}, nextID: 1, worldID: worldID}
}

func (f *fakeStore) UpsertObject(_ context.Context, o *storage.WorldObject) error {
	if o.ID == 0 {
		o.ID = f.nextID
		f.nextID++
	} else if _, ok := f.rows[o.ID]; !ok {
		return storage.ErrNotFound
	}
	o.WorldID = f.worldID
	f.rows[o.ID] = *o
	return nil
}

func (f *fakeStore) DeleteObject(_ context.Context, id int64) error {
	if _, ok := f.rows[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeStore) startWorld(t *testing.T, worldID int64) *Server {
	t.Helper()
	spec := WorldSpec{ID: worldID, Name: "ashfen", Seed: 20260909, WW: WorldW, WH: WorldH}
	s := NewServerWorld(spec)
	s.AttachWorldStore(f, worldID)
	return s
}

func (f *fakeStore) has(id int64) bool {
	_, ok := f.rows[id]
	return ok
}

func gave(t *testing.T, err error, why string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", why, err)
	}
}

func TestPlaceObjectPersistsAndApplies(t *testing.T) {
	fs := newFakeStore(7)
	s := fs.startWorld(t, 7)

	// an enemy group placed through the tool verb: row lands + live dot
	obj, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectEnemyGroup, Name: "unshriven band",
		Data: json.RawMessage(`{"count":3,"level":2}`)})
	gave(t, err, "place enemy group")
	if obj.ID == 0 {
		t.Fatal("no ID assigned by the store")
	}
	if fs.rows[obj.ID].Name != "unshriven band" || fs.rows[obj.ID].WorldID != 7 {
		t.Fatalf("row not persisted: %+v", fs.rows[obj.ID])
	}
	found := false
	for _, e := range s.Enemies() {
		if e.Name == "unshriven band" && e.Count == 3 && e.Level == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("placed group missing or stats wrong: %+v", s.Enemies())
	}

	// friendly placement takes the keeper dot (first one wins)
	_, err = s.PlaceObject(ObjectSpec{Kind: storage.ObjectVillage, Name: "ashfen square"})
	gave(t, err, "place village")
	if s.state.npcDot.Name != "ashfen square" {
		t.Fatalf("village did not take the keeper dot: %+v", s.state.npcDot)
	}

	// a second friendly one stays recorded but does not steal the dot
	_, err = s.PlaceObject(ObjectSpec{Kind: storage.ObjectMerchant, Name: "second peddler"})
	gave(t, err, "place second merchant")
	if s.state.npcDot.Name != "ashfen square" {
		t.Fatalf("second friendly stole the dot: %+v", s.state.npcDot)
	}
	if len(s.Objects(storage.ObjectMerchant)) != 1 {
		t.Fatalf("merchant rows wrong: %+v", s.Objects(storage.ObjectMerchant))
	}
}

func TestTerrainEditVerb(t *testing.T) {
	fs := newFakeStore(3)
	s := fs.startWorld(t, 3)
	sum := s.WorldSummary()
	x, y := sum.SpawnX+1, sum.SpawnY

	obj, err := s.TerrainEdit(TerrainEditSpec{Name: "marker fence", Tiles: []TileEdit{{X: x, Y: y, G: "T"}}})
	gave(t, err, "terrain edit")
	if fs.rows[obj.ID].Kind != storage.ObjectEdit {
		t.Fatalf("edit row kind wrong: %+v", fs.rows[obj.ID])
	}
	if !strings.Contains(s.state.tiles[y], "T") || s.state.tiles[y][x] != 'T' {
		t.Fatalf("glyph not applied: %q at %d", s.state.tiles[y], x)
	}

	// and a replay (resize) must re-apply it deterministically
	s.Resize("fp", WorldW+6, WorldH+2)
	if s.state.tiles[y][x] != 'T' {
		t.Fatalf("edit not replayed after resize at %d,%d", x, y)
	}
	if len(s.Objects(storage.ObjectEdit)) != 1 {
		t.Fatalf("edit row lost: %+v", s.Objects(storage.ObjectEdit))
	}
}

func TestUpdateAndRemoveObject(t *testing.T) {
	fs := newFakeStore(9)
	s := fs.startWorld(t, 9)

	obj, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectEnemyGroup, Name: "held choir",
		Data: json.RawMessage(`{"count":2,"level":1}`)})
	gave(t, err, "place")

	// update: stats change and the readback reflects the new payload
	patched, err := s.UpdateObject(obj.ID, ObjectPatch{
		Data: rawMsg(`{"count":5,"level":4}`),
	})
	gave(t, err, "update")
	if patched.ID != obj.ID {
		t.Fatalf("update landed on the wrong row: %+v", patched)
	}
	if !hasGroupWithStats(s, "held choir", 5, 4) {
		t.Fatalf("updated stats not live: %+v", s.Enemies())
	}
	if fs.rows[obj.ID].Payload == nil || json.RawMessage(fmt.Sprint(fs.rows[obj.ID].Payload)) == nil {
		t.Fatalf("payload not persisted")
	}

	// remove: live dot AND authored row drop together
	gave(t, s.RemoveObject(obj.ID), "remove")
	if fs.has(obj.ID) {
		t.Fatalf("row survived removal: %+v", fs.rows[obj.ID])
	}
	if hasGroupWithStats(s, "held choir", 5, 4) {
		t.Fatalf("live dot survived removal: %+v", s.Enemies())
	}
	if err := s.RemoveObject(obj.ID); err == nil {
		t.Fatal("second removal must report not-found (no double-free)")
	}

	// despawn through the enemy verb also drops the authored row
	obj2, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectEnemyGroup, Name: "rust hounds"})
	gave(t, err, "place 2")
	var liveID int
	for _, e := range s.Enemies() {
		if e.Name == "rust hounds" {
			liveID = e.ID
		}
	}
	if !s.DespawnEnemy(liveID) {
		t.Fatal("despawn failed")
	}
	if fs.has(obj2.ID) {
		t.Fatalf("despawn did not drop the authored row: %+v", fs.rows[obj2.ID])
	}
}

func TestPlaceRejectsBadSpecs(t *testing.T) {
	fs := newFakeStore(5)
	s := fs.startWorld(t, 5)

	if _, err := s.PlaceObject(ObjectSpec{Kind: "castle", Name: "x"}); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
	if _, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectMerchant}); err == nil {
		t.Fatal("missing name must be rejected")
	}
	off := 99999
	if _, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectMerchant, Name: "x", X: &off, Y: &off}); err == nil {
		t.Fatal("out-of-bounds anchor must be rejected")
	}
	// water tile rejected: find one
	wx, wy := waterTile(s.state)
	if wx < 0 {
		t.Skip("seed has no water")
	}
	if _, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectMerchant, Name: "x", X: &wx, Y: &wy}); err == nil {
		t.Fatal("water anchor must be rejected")
	}
}

func TestSeedFlowStaysMemoryOnly(t *testing.T) {
	// classic seed-env server (no loaded spec): tool verbs stay live-only
	s := NewServer()
	s.wstore = newFakeStore(0) // attached with no persisted world
	obj, err := s.PlaceObject(ObjectSpec{Kind: storage.ObjectEnemyGroup, Name: "transient"})
	if err != nil {
		t.Fatalf("memory-mode placement failed: %v", err)
	}
	if obj.ID == 0 {
		t.Fatal("expected an ephemeral readback id")
	}
	if s.loaded != nil {
		t.Fatal("seed-flow server must not gain a loaded spec")
	}
}

// --- helpers -----------------------------------------------------------------

func rawMsg(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

func hasGroupWithStats(s *Server, name string, count, level int) bool {
	for _, e := range s.Enemies() {
		if e.Name == name && e.Count == count && e.Level == level {
			return true
		}
	}
	return false
}

func waterTile(w *worldState) (int, int) {
	for y, row := range w.tiles {
		for x, ch := range row {
			if ch == '~' {
				return x, y
			}
		}
	}
	return -1, -1
}
