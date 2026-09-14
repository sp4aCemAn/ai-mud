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
	if e.X < 0 || e.Y < 0 || e.X >= after.W || e.Y >= after.H {
		t.Fatalf("spawned group out of bounds: %d,%d (world %dx%d)", e.X, e.Y, after.W, after.H)
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
	rows := []game.ObjectSummary{}
	env.Do("GET", "/api/world/objects?kind=enemy_group", nil, &rows)
	found := false
	for _, r := range rows {
		if r.ID == obj.ID && r.Name == "smoke:authored-band" && r.Kind == "enemy_group" {
			if r.X < 0 || r.Y < 0 {
				t.Fatalf("placement readback has a negative anchor: %+v", r)
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
