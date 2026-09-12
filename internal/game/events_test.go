package game

import (
	"testing"
)

func TestSpawnEnemyGroupRejectsGarbage(t *testing.T) {
	s := NewServer()

	_, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "", Count: 2, Level: 1})
	if err == nil || err.Error() != "name required" {
		t.Fatalf("empty name should fail with 'name required', got %v", err)
	}

	_, err = s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "x", Count: 0, Level: 1})
	if err == nil {
		t.Fatal("count 0 must fail")
	}

	_, err = s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "x", Count: 99, Level: 1})
	if err == nil {
		t.Fatal("count 99 must fail")
	}

	if _, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{
		Name: "half anchor", Count: 2, Level: 1, X: intPtr(3),
	}); err == nil || err.Error() != "both anchor coords required (or neither)" {
		t.Fatalf("half anchor must be rejected, got %v", err)
	}
	_, err = s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "x", Count: 2, Level: 1, X: intPtr(9999), Y: intPtr(3)})
	if err == nil {
		t.Fatal("out-of-world anchor must fail")
	}
}

func TestSpawnEnemyGroupAnchoredAndRandom(t *testing.T) {
	s := NewServer()
	s.DebugFlattenWorld() // empty walkable field, deterministic

	e, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "black choir", Count: 2, Level: 1, X: intPtr(3), Y: intPtr(7)})
	if err != nil {
		t.Fatalf("anchored spawn: %v", err)
	}
	if e.X != 3 || e.Y != 7 {
		t.Fatalf("anchored spawn misplaced: %d,%d", e.X, e.Y)
	}
	if e.Count != 2 || e.Level != 1 {
		t.Fatalf("spec not honored: %+v", e)
	}
	if e.HP <= 0 || e.MaxHP <= 0 {
		t.Fatalf("hp not derived: %d/%d", e.HP, e.MaxHP)
	}

	// random spawn lands somewhere in-bounds with sane stats
	rest, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "rust hounds", Count: 4, Level: 3, X: intPtr(0), Y: intPtr(0)})
	if err != nil {
		t.Fatalf("random spawn: %v", err)
	}
	w := s.state
	if rest.X < 0 || rest.Y < 0 || rest.X >= w.ww || rest.Y >= w.wh {
		t.Fatalf("random spawn out of world: %d,%d", rest.X, rest.Y)
	}

	// the readback sees both
	rows := s.Enemies()
	if len(rows) != 2 {
		t.Fatalf("Enemies() should report 1 anchored + 1 random group, got %+v", rows)
	}

	// the world version moved (renderers pick it up next frame)
	before := s.WorldSummary().Version
	des := s.DespawnEnemy(e.ID)
	if !des {
		t.Fatal("despawn of a live group must report true")
	}
	if s.WorldSummary().Version <= before {
		t.Fatal("despawn must bump the render version")
	}
	after := s.Enemies()
	if len(after) != 1 || after[0].ID == e.ID {
		t.Fatalf("despawn did not remove the group: %+v", after)
	}
	if s.DespawnEnemy(e.ID) {
		t.Fatal("second despawn must report false")
	}
}

func TestSpawnRejectsAndAcceptsWaterAwareAnchor(t *testing.T) {
	s := NewServer()
	// real terrain: find a water tile and try to anchor there
	w := s.state
	var wx, wy int
	found := false
	for y := 0; y < w.wh && !found; y++ {
		for x := 0; x < w.ww && !found; x++ {
			if !walkable(w.tiles, x, y) {
				wx, wy, found = x, y, true
			}
		}
	}
	if !found {
		t.Skip("terrain has no water tile to test with")
	}
	if _, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{Name: "wet ghouls", Count: 1, Level: 1, X: intPtr(wx), Y: intPtr(wy)}); err == nil {
		t.Fatal("water anchor must be rejected")
	}
}

func TestAnnounceAndWorldSummary(t *testing.T) {
	s := NewServer()
	sum := s.WorldSummary()
	if sum.W == 0 || sum.H == 0 || sum.EnemyCount == 0 {
		t.Fatalf("summary should see a populated world: %+v", sum)
	}
	if sum.MerchantX < 0 {
		t.Fatalf("merchant dot missing: %+v", sum.MerchantY)
	}
	s.Announce("") // no-op, must not panic
}

func intPtr(n int) *int { return &n }
