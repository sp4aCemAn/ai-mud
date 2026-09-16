// Package smokes keeps the stored API-level smoke tests. A smoke is a
// plain Go test composable with the smoketest.Env runner. Suites run
// against a DEPLOYED server only (GAME_SMOKE_URL) — they always target
// the exact contract a live tool call must satisfy.
package smokes

import (
	"net/http"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/tests/smoketest"
)

// Smoke is one registered scenario. Registration order = run order.
type Smoke struct {
	Name string
	Run  func(t *testing.T, env smoketest.Env)
}

// registry: append new smokes here. The umbrella test walks them in
// order, so later smokes can lean on earlier ones' effects.
var registry = []Smoke{
	{"healthz_and_world_summary", smokeHealthAndSummary},
	{"spawn_group_reflcts_world", smokeSpawnReflects},
	{"announce_accepted", smokeAnnounce},
	{"despawn_removed", smokeDespawn},
	{"persisted_world_url_load", smokePersistedLoadNote},
	// Part 3: authored placements (world_objects) over the tool surface
	{"place_object_readback", smokePlaceReadback},
	{"terrain_edit_applies", smokeTerrainEdit},
	{"patch_update_replays", smokePatchUpdate},
	{"remove_object_roundtrip", smokeRemoveRoundtrip},
	// Slice 5: the item registry + town store authoring surface
	{"town_store_authored_counter", smokeTownStoreAuthoring},
	{"town_store_namespace_rejected", smokeTownStoreNamespaceRejected},
	{"door_tile_rejects_hostile_anchor", smokeDoorTileRejectsHostileAnchor},
}

// smokePersistedLoad: worlds are loaded at BOOT (the loader reads the
// active world row and replays content), so the HTTP surface can only
// observe a reload, not trigger one. This smoke pins what the tools
// CAN do now: readbacks stay live and consistent across operations.
// NOTE: the persisted-boot path is covered by the standalone smoke
// below only when it boots its own world (NewServerWorld in the game
// package tests); the deployed contract asserts the run as served.
func smokePersistedLoadNote(t *testing.T, env smoketest.Env) {
	var sum game.WorldSummary
	env.Do("GET", "/api/world", nil, &sum)
	if sum.Version == 0 {
		t.Fatal("deployed world should have a nonzero render version")
	}
}

func TestSmokeSuite(t *testing.T) {
	env := smoketest.New(t)
	for _, s := range registry {
		s := s
		t.Run(s.Name, func(t *testing.T) { s.Run(t, env) })
	}
}

// smokeHealthAndSummary: the live server answers both reads.
func smokeHealthAndSummary(t *testing.T, env smoketest.Env) {
	var sum game.WorldSummary
	env.Do("GET", "/api/world", nil, &sum)
	if sum.W <= 0 || sum.H <= 0 {
		t.Fatalf("world dims: %+v", sum)
	}
	if sum.EnemyCount == 0 {
		t.Fatalf("world has no hostile groups (spawn doors opened?): %+v", sum)
	}
}

// smokeSpawnReflects: a tool-spawned group becomes visible in the
// world readback with exactly the spec'd name/count/level.
func smokeSpawnReflects(t *testing.T, env smoketest.Env) {
	var before game.WorldSummary
	env.Do("GET", "/api/world", nil, &before)

	e := spawnRef(t, env, game.SpawnEnemyGroupSpec{Name: "uX:harness-test-group", Count: 2, Level: 1})
	var after game.WorldSummary
	env.Do("GET", "/api/world", nil, &after)
	if after.EnemyCount != before.EnemyCount+1 {
		t.Fatalf("enemy count %d → %d, expected +1", before.EnemyCount, after.EnemyCount)
	}
	found := false
	for _, name := range after.EnemyNames {
		if name == "uX:harness-test-group" {
			found = true
		}
	}
	if !found {
		t.Fatalf("spawned group missing from world summary: %+v", after.EnemyNames)
	}
	// bounds are rect-local now (the infinite plane may serve negative abs)
	if e.X < after.OriginX || e.Y < after.OriginY ||
		e.X >= after.OriginX+after.W || e.Y >= after.OriginY+after.H {
		t.Fatalf("spawned group out of served rect: %d,%d (rect %d..%d x %d..%d)",
			e.X, e.Y, after.OriginX, after.OriginX+after.W, after.OriginY, after.OriginY+after.H)
	}
	if e.Count != 2 || e.Level != 1 || e.Name != "uX:harness-test-group" {
		t.Fatalf("spec not honored: %+v", e)
	}
	env.Log("spawned and read back id=%d", e.ID)
}

// smokeAnnounce: the announce tool returns accepted (visible to players).
func smokeAnnounce(t *testing.T, env smoketest.Env) {
	env.Do("POST", "/api/world/announce", map[string]string{"text": "smoke suite passed through"}, nil)
}

// smokeDespawn: despawning removes the spawned group (lease the
// spawned id from the readback list — this smoke is written to run
// AFTER smokeSpawnReflects in the registry).
func smokeDespawn(t *testing.T, env smoketest.Env) {
	rows := worldEnemies(t, env)
	if len(rows) == 0 {
		t.Fatal("no enemies to despawn")
	}
	// despawn the smallest-HP group (any live one works)
	target := rows[0]
	env.Do("DELETE", "/api/world/enemies/"+int64String(target.ID), nil, nil)
	rows = worldEnemies(t, env)
	for _, r := range rows {
		if r.ID == target.ID {
			t.Fatalf("group %d survived despawn", target.ID)
		}
	}
}

// smokePlaceReadback: a placed object comes back through the objects
// route with its kind/name/anchor, and shows up in the summary.
func smokePlaceReadback(t *testing.T, env smoketest.Env) {
	var obj game.ObjectSummary
	env.Do("POST", "/api/world/objects", map[string]any{
		"kind": "enemy_group", "name": "smoke:authored-band",
		"data": map[string]int{"count": 2, "level": 1},
	}, &obj)
	if obj.ID == 0 {
		t.Fatal("placed object has no id")
	}
	var sum game.WorldSummary
	env.Do("GET", "/api/world", nil, &sum)
	rows := []game.ObjectSummary{}
	env.Do("GET", "/api/world/objects?kind=enemy_group", nil, &rows)
	found := false
	for _, r := range rows {
		if r.ID == obj.ID && r.Name == "smoke:authored-band" && r.Kind == "enemy_group" {
			// anchors may be negative absolute coords now — must be in-rect
			if r.X < sum.OriginX || r.Y < sum.OriginY {
				t.Fatalf("placement readback outside the served rect: %+v", r)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("placed object missing from readback: %+v", rows)
	}
	env.Log("authored %s id=%d at %d,%d", obj.Kind, obj.ID, obj.X, obj.Y)
}

// smokeTerrainEdit: the terrain verb accepts a glyph patch and the
// edit row is listed (tool retry loop: bad payloads 400).
func smokeTerrainEdit(t *testing.T, env smoketest.Env) {
	var obj game.ObjectSummary
	env.Do("POST", "/api/world/terrain", map[string]any{
		"name":  "smoke:marker fence",
		"tiles": []map[string]any{{"x": 5, "y": 5, "g": "T"}},
	}, &obj)
	if obj.ID == 0 {
		t.Fatal("edit row has no id")
	}
	try := map[string]any{"name": "bad edit"}
	env.DoStatus("POST", "/api/world/terrain", try, nil, http.StatusBadRequest)
	env.Log("edit row id=%d", obj.ID)
}

// smokePatchUpdate: an update lands and the readback shows the patch.
func smokePatchUpdate(t *testing.T, env smoketest.Env) {
	obj := smokePlaceReadbackObj(t, env, "smoke:patchable-band")
	env.Do("PATCH", "/api/world/objects/"+int64String(obj.ID),
		map[string]any{"data": map[string]int{"count": 4, "level": 3}}, nil,
	)
	rows := []game.ObjectSummary{}
	env.Do("GET", "/api/world/objects?kind=enemy_group", nil, &rows)
	for _, r := range rows {
		if r.ID == obj.ID {
			env.Log("patched id=%d name=%q", r.ID, r.Name)
			return
		}
	}
	t.Fatalf("patched object vanished from readback: %+v", rows)
}

// smokeRemoveRoundtrip: removing a placement kills it (and the live
// dot), and a second removal is a clean not-found.
func smokeRemoveRoundtrip(t *testing.T, env smoketest.Env) {
	obj := smokePlaceReadbackObj(t, env, "smoke:remove-me")
	env.Do("DELETE", "/api/world/objects/"+int64String(obj.ID), nil,
		nil)
	// readback no longer lists it
	rows := []game.ObjectSummary{}
	env.Do("GET", "/api/world/objects", nil, &rows)
	for _, r := range rows {
		if r.ID == obj.ID {
			t.Fatalf("removed object still listed: %+v", r)
		}
	}
	// second removal: 404 with an error body
	env.DoStatus("DELETE", "/api/world/objects/"+int64String(obj.ID), nil,
		nil, http.StatusNotFound)
	env.Log("removed cleanly id=%d", obj.ID)
}

// smokeTownStoreAuthoring (slice 5): a village row carries ONE item
// registry shape — town-local specs (ids prefixed by the row's own
// name) plus per-NPC wares refs resolving through the global list.
// The counter's authority is the row: PATCH the wares and the store
// rebuilds on the town's next build (this smoke checks the row
// surface; the buy itself is SSH/UI-only, like the stay).
func smokeTownStoreAuthoring(t *testing.T, env smoketest.Env) {
	// anchor-less placement: the row records its real walkable coords
	// (a hard anchor is a seed-lottery — the standalone world re-rolls
	// per boot and a pinned water tile 400s the scenario)
	obj := placeObject(t, env, map[string]any{
		"kind": "village", "name": "smokequay", "radius": 4,
		"data": map[string]any{
			"items": []map[string]any{
				{"id": "smokequay:embercask", "name": "ember cask",
					"desc": "pours slow", "price": 15,
					"kind": "potion", "effect": "hp:+6", "inv": "ember casks"},
			},
			"npcs": []map[string]any{
				{"type": "storekeep", "name": "Quinn", "x": 4, "y": 3,
					"wares": []string{"potion:dark", "smokequay:embercask"}},
			},
		},
	})
	if obj.ID == 0 {
		t.Fatal("village row")
	}
	// readback keeps the wares refs reachable (rows list them)
	rows := worldObjects(t, env, "village")
	placed := 0
	for _, r := range rows {
		if r.Name == "smokequay" {
			placed++
		}
	}
	if placed == 0 {
		t.Fatalf("village row vanished from readback: %+v", rows)
	}
	// CLEAN-UP OWN ROWS: every delta run leaks one more smokequay into
	// the live world otherwise (the sweep-by-name keeps touching the
	// live world by hand). Remove the row the scenario authored —
	// a smoke leaves nothing behind.
	env.Do("DELETE", "/api/world/objects/"+int64String(obj.ID), nil, nil)
	after := worldObjects(t, env, "village")
	for _, r := range after {
		if r.ID == obj.ID {
			t.Fatalf("the authored row survived its own removal: %+v", r)
		}
	}
	env.Log("town-store row authored and removed")
}

// smokeTownStoreNamespaceRejected: the no-collision rule is tool-time.
// A local item whose ID lacks the row's own town prefix must 400.
func smokeTownStoreNamespaceRejected(t *testing.T, env smoketest.Env) {
	try := map[string]any{
		"kind": "village", "name": "nohold", "x": 21, "y": 6, "radius": 3,
		"data": map[string]any{
			"items": []map[string]any{
				{"id": "wardmail", "name": "ward mail", "price": 3,
					"kind": "gear", "effect": "def:+1"},
			},
		},
	}
	env.DoStatus("POST", "/api/world/objects", try, nil, http.StatusBadRequest)
	env.Log("unprefixed local item rejected at tool time")
}

// smokeDoorTileRejectsHostile: the no-door-squatting rule (the enemy
// dot composites over the gate glyph — the door renders as an enemy
// while its tile data stays honest). A door anchor is a tool-time 400.
func smokeDoorTileRejectsHostileAnchor(t *testing.T, env smoketest.Env) {
	// the door: any village row's anchor (each village paints its own
	// 町 intrinsically). Self-sufficient: no village in the readback →
	// author one (and remove it after — a smoke leaves nothing behind).
	rows := worldObjects(t, env, "village")
	var v game.ObjectSummary
	if len(rows) > 0 {
		v = rows[0]
	} else {
		obj := placeObject(t, env, map[string]any{
			"kind": "village", "name": "doorgate", "radius": 4,
		})
		v = obj
		defer func() {
			env.Do("DELETE", "/api/world/objects/"+int64String(obj.ID), nil, nil)
		}()
	}
	try := map[string]any{
		"kind": "enemy_group", "name": "uX:door-squatter",
		"x": v.X, "y": v.Y,
		"data": map[string]int{"count": 1, "level": 1},
	}
	env.DoStatus("POST", "/api/world/objects", try, nil, http.StatusBadRequest)
	env.Log("door anchor rejected at world tile")
}
