package smokes

import (
	"fmt"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/game"
	"github.com/sp4aceman/ai-mud/tests/smoketest"
)

// readback helpers -------------------------------------------------------------

// worldEnemies snapshots the live enemy rows via the tool readback.
func worldEnemies(t *testing.T, env smoketest.Env) []game.EnemySummary {
	t.Helper()
	var rows []game.EnemySummary
	env.Do("GET", "/api/world/enemies", nil, &rows)
	return rows
}

// envWorld snapshots the world summary.
func envWorld(t *testing.T, env smoketest.Env) game.WorldSummary {
	t.Helper()
	var sum game.WorldSummary
	env.Do("GET", "/api/world", nil, &sum)
	return sum
}

// spawnRef performs a POST spawn and returns the created row (Env.Do
// fails the test on a non-2xx, so only the success path reaches here).
func spawnRef(t *testing.T, env smoketest.Env, spec game.SpawnEnemyGroupSpec) game.Enemy {
	t.Helper()
	var e game.Enemy
	env.Do("POST", "/api/world/enemies", spec, &e)
	return e
}

// int64String formats an id of either width for path building.
func int64String[T ~int | ~int64](n T) string { return fmt.Sprintf("%d", n) }

// worldObjects snapshots the authored placements via the objects route.
func worldObjects(t *testing.T, env smoketest.Env, kinds ...string) []game.ObjectSummary {
	t.Helper()
	path := "/api/world/objects"
	if len(kinds) > 0 {
		path += "?kind=" + kinds[0]
	}
	var rows []game.ObjectSummary
	env.Do("GET", path, nil, &rows)
	return rows
}

// placeObject places one authored row and returns its readback.
func placeObject(t *testing.T, env smoketest.Env, body map[string]any) game.ObjectSummary {
	t.Helper()
	var obj game.ObjectSummary
	env.Do("POST", "/api/world/objects", body, &obj)
	return obj
}

// smokePlaceReadbackObj is the patch/remove smokes' lean helper: place
// one anchored enemy group under a distinctive name.
func smokePlaceReadbackObj(t *testing.T, env smoketest.Env, name string) game.ObjectSummary {
	t.Helper()
	return placeObject(t, env, map[string]any{
		"kind": "enemy_group", "name": name,
		"data": map[string]int{"count": 2, "level": 1},
	})
}
