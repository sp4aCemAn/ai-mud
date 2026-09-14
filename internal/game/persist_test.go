package game

import (
	"encoding/json"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

func TestNewServerWorldReplaysObjects(t *testing.T) {
	s := NewServerWorld(WorldSpec{
		Name: "ashfen",
		Seed: 20260909,
		WW:   WorldW,
		WH:   WorldH,
		Objects: []storage.WorldObject{
			{
				Kind:    storage.ObjectEnemyGroup,
				Name:    "black choir",
				HomeX:   5,
				HomeY:   5,
				Payload: json.RawMessage(`{"count":2,"level":2}`),
			},
			{
				Kind:    storage.ObjectMerchant,
				Name:    "ashfen peddler",
				HomeX:   3, // deliberately unfriendly spawn — engine should pick a spot
				HomeY:   3,
				Payload: json.RawMessage(`{"wares":["rope"]}`),
			},
		},
	})

	// the authored enemy group is live with its authored stats
	rows := s.Enemies()
	found := false
	for _, e := range rows {
		if e.Name == "black choir" {
			found = true
			if e.Count != 2 || e.Level != 2 {
				t.Fatalf("spec not honored: %+v", e)
			}
			if e.X < 0 || e.Y < 0 || e.X >= s.state.ww || e.Y >= s.state.wh {
				t.Fatalf("authored group out of world: %d,%d", e.X, e.Y)
			}
		}
	}
	if !found {
		t.Fatalf("authored group missing from readback: %+v", rows)
	}

	// the first friendly placement took the merchant dot
	if s.state.npcDot.Name != "ashfen peddler" {
		t.Fatalf("merchant not placed: %+v", s.state.npcDot)
	}
	if s.state.npcDot.X < 0 || !walkable(s.state.tiles, s.state.npcDot.X, s.state.npcDot.Y) {
		t.Fatalf("merchant not on a walkable tile: %+v", s.state.npcDot)
	}

	// summary agrees
	sum := s.WorldSummary("")
	if sum.MerchantX != s.state.npcDot.X || sum.EnemyCount < 1 {
		t.Fatalf("summary mismatch: %+v", sum)
	}
}

func hasEnemyNamed(sum WorldSummary, name string) bool {
	for _, n := range sum.EnemyNames {
		if n == name {
			return true
		}
	}
	return false
}

func TestPersistedSpawnNeverWater(t *testing.T) {
	// a rescued row that THOUGHT the spawn was walkable may be stale —
	// the chunk plane's look differs from the old finite generator's
	spec := WorldSpec{Name: "wet gate", Seed: 990099, WW: WorldW, WH: WorldH}
	s := NewServerWorld(spec)
	// find SOME water on the plane; pin the record's spawn there
	wx, wy := -1, -1
	for y := 0; y < s.state.wh && wx < 0; y++ {
		for x := 0; x < s.state.ww && wx < 0; x++ {
			if !s.state.walkableAt(x, y) {
				wx, wy = x, y
			}
		}
	}
	if wx < 0 {
		t.Skip("seed's boot rect has no water")
	}
	wc := WorldSpec{Name: "wet gate", Seed: spec.Seed, WW: spec.WW, WH: spec.WH,
		SpawnX: wx, SpawnY: wy}
	s2 := NewServerWorld(wc)
	if !s2.state.walkableAt(s2.state.spawnX, s2.state.spawnY) {
		t.Fatalf("boot spawned in water at %d,%d", s2.state.spawnX, s2.state.spawnY)
	}
}

func TestResizeKeepsPersistedContent(t *testing.T) {
	w := WorldSpec{
		Name: "held-field",
		Seed: 1001,
		WW:   WorldW,
		WH:   WorldH,
		Objects: []storage.WorldObject{
			{
				Kind:  storage.ObjectEnemyGroup,
				Name:  "held group",
				HomeX: 4,
				HomeY: 2,
			},
		},
	}
	s := NewServerWorld(w)
	sum := s.WorldSummary("")
	if !hasEnemyNamed(sum, "held group") {
		t.Fatal("authored group should be present at boot")
	}
	vBefore := sum.Version

	// resize to a bigger board: content must come back, same name
	s.Resize("fp", WorldW+10, WorldH+4)
	sum = s.WorldSummary("")
	if !hasEnemyNamed(sum, "held group") {
		t.Fatal("authored group must survive resize replay")
	}
	if sum.W == 40 || sum.H == 12 {
		t.Fatalf("world did not resize: %+v", sum)
	}

	// a second resize keeps it as well (seed replay is idempotent)
	s.Resize("fp", WorldW-8, WorldH-2)
	sum = s.WorldSummary("")
	if !hasEnemyNamed(sum, "held group") {
		t.Fatal("authored group must survive repeated resizes")
	}
	if sum.Version <= vBefore {
		t.Fatal("resize must bump the render version")
	}
}
