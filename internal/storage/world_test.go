package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestIntegrationWorlds covers part 1 of the world-persistence plan:
// world CRUD, content CRUD (world_objects + object_state), snapshots,
// and the one-active-world invariant — against the real relational DB.
//
//	STORAGE_INTEGRATION=1 go test ./internal/storage -v -run TestIntegrationWorlds
func TestIntegrationWorlds(t *testing.T) {
	integrationEnabled(t)
	cfg := testCfg()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := connectPostgres(ctx, cfg.PostgresDSN, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer rel.Close()

	// unique names per run (name is UNIQUE across the table)
	unique := fmt.Sprintf("testworld-%d", time.Now().UnixNano())
	cleanup := func() {
		cards, _ := rel.ListWorlds(ctx)
		for _, c := range cards {
			if c.Name == unique || c.Name == unique+"-b" {
				_ = rel.DeleteWorld(ctx, c.ID)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// --- create + fetch ---
	w1 := &World{Name: unique, Seed: 424242, WW: 98, WH: 25, SpawnX: 10, SpawnY: 11}
	if err := rel.CreateWorld(ctx, w1); err != nil {
		t.Fatalf("create world: %v", err)
	}
	if w1.ID == 0 {
		t.Fatal("world id not assigned")
	}
	got, err := rel.World(ctx, w1.ID)
	if err != nil {
		t.Fatalf("fetch world: %v", err)
	}
	if got.Seed != 424242 || got.WW != 98 || got.WH != 25 || got.SpawnX != 10 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// duplicate name → ErrConflict
	if err := rel.CreateWorld(ctx, &World{
		Name: unique, Seed: 1, WW: 40, WH: 12, SpawnX: 0, SpawnY: 0,
	}); err != ErrConflict {
		t.Fatalf("duplicate name should be ErrConflict, got %v", err)
	}

	// missing world → ErrNotFound
	if _, err := rel.World(ctx, 99999999); err != ErrNotFound {
		t.Fatalf("missing world should be ErrNotFound, got %v", err)
	}

	// --- content: insert → list → edit-by-id → kind filter ---
	obj := &WorldObject{
		WorldID: w1.ID,
		Kind:    ObjectVillage,
		Name:    "ashfen",
		Seed:    9,
		HomeX:   20,
		HomeY:   15,
		Radius:  6,
		Tiles:   json.RawMessage(`[{"x": 3, "y": 4, "kind": "house"}]`),
		Payload: json.RawMessage(`{"wares":["rope","bread"]}`),
	}
	if err := rel.UpsertObject(ctx, obj); err != nil {
		t.Fatalf("insert object: %v", err)
	}
	if obj.ID == 0 {
		t.Fatal("object id not assigned")
	}

	objs, err := rel.ListObjects(ctx, w1.ID)
	if err != nil || len(objs) != 1 {
		t.Fatalf("list objects: %+v err=%v", objs, err)
	}
	var gotTiles []map[string]any
	if err := json.Unmarshal(objs[0].Tiles, &gotTiles); err != nil {
		t.Fatalf("tiles not json: %v", err)
	}
	if len(gotTiles) != 1 || gotTiles[0]["kind"] != "house" || objs[0].Name != "ashfen" || objs[0].HomeX != 20 {
		t.Fatalf("round-trip mismatch: %+v", objs[0])
	}
	var gotData map[string]any
	if err := json.Unmarshal(objs[0].Payload, &gotData); err != nil || gotData["wares"] == nil {
		t.Fatalf("data payload: %+v", objs[0].Payload)
	}

	obj.Name = "ashfen-once"
	obj.Payload = json.RawMessage(`{"wares":["rope"]}`)
	if err := rel.UpsertObject(ctx, obj); err != nil {
		t.Fatalf("edit object: %v", err)
	}
	objs, _ = rel.ListObjects(ctx, w1.ID)
	if len(objs) != 1 || objs[0].Name != "ashfen-once" {
		t.Fatalf("edit did not persist: %+v", objs)
	}

	enemy := &WorldObject{WorldID: w1.ID, Kind: ObjectEnemyGroup, Name: "black choir", HomeX: 1, HomeY: 1}
	if err := rel.UpsertObject(ctx, enemy); err != nil {
		t.Fatalf("insert enemy group: %v", err)
	}
	villages, err := rel.ListObjects(ctx, w1.ID, ObjectVillage)
	if err != nil || len(villages) != 1 {
		t.Fatalf("kind filter should return only the village: %+v err=%v", villages, err)
	}
	bad := &WorldObject{WorldID: w1.ID, Kind: "hut", Name: "x"}
	if err := rel.UpsertObject(ctx, bad); err == nil {
		t.Fatal("unknown kind must be rejected")
	}

	// --- object runtime state (upsert + overwrite + miss) ---
	st := &ObjectState{ObjectID: obj.ID, State: json.RawMessage(`{"hp":3}`)}
	if err := rel.PutObjectState(ctx, st); err != nil {
		t.Fatalf("put state: %v", err)
	}
	gotSt, err := rel.GetObjectState(ctx, obj.ID)
	if err != nil || stateHP(t, gotSt.State) != 3 {
		t.Fatalf("get state: %+v err=%v", gotSt, err)
	}
	st.State = json.RawMessage(`{"hp":9}`)
	if err := rel.PutObjectState(ctx, st); err != nil {
		t.Fatalf("put state 2: %v", err)
	}
	gotSt, _ = rel.GetObjectState(ctx, obj.ID)
	if stateHP(t, gotSt.State) != 9 {
		t.Fatalf("state overwrite failed: %s", gotSt.State)
	}
	if _, err := rel.GetObjectState(ctx, 88888888); err != ErrNotFound {
		t.Fatalf("missing state should be ErrNotFound, got %v", err)
	}

	// --- snapshots: insert two, latest wins, delete rolls back ---
	snapA := &WorldSnapshot{WorldID: w1.ID, Label: "checkpoint a", Terrain: ",,,"}
	if err := rel.SaveSnapshot(ctx, snapA); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	bMeta, _ := json.Marshal(map[string]int{"n": 1})
	snapB := &WorldSnapshot{WorldID: w1.ID, Label: "checkpoint b", Terrain: ".,.", Meta: bMeta}
	if err := rel.SaveSnapshot(ctx, snapB); err != nil {
		t.Fatalf("save snapshot b: %v", err)
	}
	latest, err := rel.LatestSnapshot(ctx, w1.ID)
	if err != nil || latest.Label != "checkpoint b" {
		t.Fatalf("latest snapshot: %+v err=%v", latest, err)
	}
	if err := rel.DeleteSnapshot(ctx, snapB.ID); err != nil {
		t.Fatalf("delete snapshot: %v", err)
	}
	gotLatest, err := rel.LatestSnapshot(ctx, w1.ID)
	if err != nil || gotLatest.Label != "checkpoint a" || gotLatest.Terrain != ",,," {
		t.Fatalf("latest after delete: %+v err=%v", gotLatest, err)
	}

	// --- object_state cascades with its object ---
	if err := rel.DeleteObject(ctx, obj.ID); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if err := rel.DeleteObject(ctx, obj.ID); err != ErrNotFound {
		t.Fatalf("second delete should be ErrNotFound, got %v", err)
	}
	if _, err := rel.GetObjectState(ctx, obj.ID); err != ErrNotFound {
		t.Fatalf("state should have cascaded, got %v", err)
	}

	// --- activation invariant & deletes ---
	if err := rel.ActivateWorld(ctx, w1.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	w2 := &World{Name: unique + "-b", Seed: 777, WW: 40, WH: 12, SpawnX: 5, SpawnY: 5}
	if err := rel.CreateWorld(ctx, w2); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := rel.ActivateWorld(ctx, w2.ID); err != nil {
		t.Fatalf("activate second: %v", err)
	}
	if n := countActiveWorlds(rel); n != 1 {
		t.Fatalf("expected exactly one active world, got %d", n)
	}
	if err := rel.DeleteWorld(ctx, w2.ID); err != nil {
		t.Fatalf("delete world: %v", err)
	}
	if err := rel.DeleteWorld(ctx, w2.ID); err != ErrNotFound {
		t.Fatalf("double delete should be ErrNotFound, got %v", err)
	}
	// deleting the ACTIVE world leaves none active (boot then has to
	// pick or keep an empty roster; operator re-activates explicitly)
	_, err = rel.ActiveWorld(ctx)
	if err != ErrNotFound {
		t.Fatalf("after deleting the active world there should be none, got err=%v", err)
	}
}

// countActiveWorlds checks the one-active invariant directly.
func countActiveWorlds(rel *Relational) int {
	cards, err := rel.ListWorlds(context.Background())
	if err != nil {
		return -1
	}
	n := 0
	for _, c := range cards {
		if c.IsActive {
			n++
		}
	}
	return n
}

// stateHP decodes `{"hp": n}`-shaped state for assertions.
func stateHP(t *testing.T, raw json.RawMessage) float64 {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("state not json: %v (%s)", err, string(raw))
	}
	n, _ := m["hp"].(float64)
	return n
}
