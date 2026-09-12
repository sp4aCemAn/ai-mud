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

// int64String formats an integer id for path building.
func int64String(n int) string { return fmt.Sprintf("%d", n) }
