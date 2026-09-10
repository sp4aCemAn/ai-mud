package game

import (
	"math/rand"
	"strings"
	"testing"
)

// TestTerrainEverythingReachable generates worlds repeatedly and
// asserts every dot lies in the same connected region as spawn — the
// no-dead-ends guarantee.
func TestTerrainEverythingReachable(t *testing.T) {
	for run := 0; run < 30; run++ {
		s := NewServer()
		w := s.state

		if !walkable(w.tiles, w.spawnX, w.spawnY) {
			t.Fatalf("spawn (%d,%d) is not walkable", w.spawnX, w.spawnY)
		}
		if !walkable(w.tiles, w.npcDot.X, w.npcDot.Y) {
			t.Fatalf("merchant at (%d,%d) is in the lake", w.npcDot.X, w.npcDot.Y)
		}

		// flood fill from spawn over the full snapshot's dots
		seen := map[[2]int]bool{{w.spawnX, w.spawnY}: true}
		frontier := [][2]int{{w.spawnX, w.spawnY}}
		for len(frontier) > 0 {
			c := frontier[len(frontier)-1]
			frontier = frontier[:len(frontier)-1]
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := c[0]+d[0], c[1]+d[1]
				if walkable(w.tiles, nx, ny) && !seen[[2]int{nx, ny}] {
					seen[[2]int{nx, ny}] = true
					frontier = append(frontier, [2]int{nx, ny})
				}
			}
		}
		for _, e := range w.enemies {
			if !seen[[2]int{e.X, e.Y}] {
				t.Fatalf("enemy group unreachable at (%d,%d)", e.X, e.Y)
			}
		}
		if !seen[[2]int{w.npcDot.X, w.npcDot.Y}] {
			t.Fatalf("merchant unreachable at (%d,%d)", w.npcDot.X, w.npcDot.Y)
		}
	}
}

// TestWaterBlocksMovement: water is impassable and the log says so.
func TestWaterBlocksMovement(t *testing.T) {
	s := NewServer()
	flatten(s)
	p := s.Join("fp", "guest")

	s.state.tiles[p.Y] = strings.Repeat(",", WorldW)
	row := []rune(s.state.tiles[p.Y])
	tilepos := p.X + 1
	if tilepos >= WorldW { // keep the east neighbour open elsewhere
		tilepos = p.X - 1
	}
	row[tilepos] = tileWater
	s.state.tiles[p.Y] = string(row)

	if tilepos > p.X {
		r := s.Interact("fp", 1, 0)
		if r.Player.X != p.X {
			t.Fatal("movement into water must not move the player")
		}
		if len(r.Events) == 0 || !strings.Contains(r.Events[len(r.Events)-1], "water") {
			t.Fatalf("water block should log why: %v", r.Events)
		}
	}
}

// TestFightLifecycle: bump → fight, attack kills, loot arrives, fight
// over. Seeded RNG keeps it deterministic.
func TestFightLifecycle(t *testing.T) {
	s := NewServer()
	flatten(s)
	s.state.rnd = rand.New(rand.NewSource(1))
	p := s.Join("fp", "guest")

	e := &Enemy{ID: 1, Name: "rust hounds", X: p.X + 1, Y: p.Y, Count: 2, HP: 1, MaxHP: 10, Level: 1}
	s.state.enemies[1] = e

	r := s.Interact("fp", 1, 0)
	if r.Fight == nil {
		t.Fatal("bumping an enemy dot must open a fight")
	}

	r = s.Command("fp", "attack", 0)
	if r.Fight != nil {
		t.Fatal("enemy with 1 hp must die on the first attack")
	}
	if r.Player.XP == 0 || r.Player.Coins <= p.Coins {
		t.Fatalf("kill should pay out: xp=%d coins=%d", r.Player.XP, r.Player.Coins)
	}
	if e.HP != 0 {
		t.Fatalf("dead enemy hp should be 0, got %d", e.HP)
	}

	// walking away is free again
	r = s.Interact("fp", 1, 0)
	if r.Fight != nil {
		t.Fatal("bumping the dead dot must not reopen a fight")
	}
}

// TestFightDeathRespawn: losing returns the player to spawn, halves
// the purse, and clears the fight — never a dead end.
func TestFightDeathRespawn(t *testing.T) {
	s := NewServer()
	flatten(s)
	s.state.rnd = rand.New(rand.NewSource(2))
	p := s.Join("fp", "guest")
	coinsBefore := p.Coins

	e := &Enemy{ID: 9, Name: "black choir", X: p.X + 1, Y: p.Y, Count: 4, HP: 1_000_000, MaxHP: 1_000_000, Level: 9}
	s.state.enemies[9] = e
	s.playersMu.Lock()
	s.players["fp"].HP = 1 // barely standing — one answering blow kills
	s.playersMu.Unlock()
	s.Interact("fp", 1, 0)

	r := s.Command("fp", "attack", 0)
	if r.Fight != nil {
		t.Fatal("death must clear the fight")
	}
	got := r.Player
	if got.X != WorldW/2 || got.Y != WorldH/2 {
		t.Fatalf("death should respawn at spawn, got (%d,%d)", got.X, got.Y)
	}
	if got.HP != got.MaxHP || got.Coins > coinsBefore/2+coinsBefore%2 {
		t.Fatalf("death should restore health and halve the purse: %+v", got)
	}
}

// TestStorePurchase: coin out, potion in; and closing works.
func TestStorePurchase(t *testing.T) {
	s := NewServer()
	flatten(s)
	p := s.Join("fp", "guest")
	s.playersMu.Lock()
	s.players["fp"].Coins = 50 // the starting purse can't afford much
	s.playersMu.Unlock()
	s.Command("fp", "buy", 1)

	got, _ := s.State("fp")
	if got.Coins != 38 || got.Inventory["dark potions"] != 1 {
		t.Fatalf("purchase failed: %+v", got)
	}

	r := s.Command("fp", "buy", 1)
	if r.Player.Coins != 26 || r.Player.Inventory["dark potions"] != 2 {
		t.Fatalf("after second purchase: %+v", r.Player)
	}

	r = s.Command("fp", "close", 0)
	if r.Shop != nil {
		t.Fatal("close must leave the store")
	}
	if got, _ := s.State("fp"); got.Coins != 26 {
		t.Fatalf("coins after close: %d", got.Coins)
	}
	_ = p
}

// TestTickRegen: wound players heal on the slow clock.
func TestTickRegen(t *testing.T) {
	s := NewServer()
	flatten(s)
	p := s.Join("fp", "guest")
	p.HP = 3
	p.Mana = 0
	s.playersMu.Lock()
	sp := s.players["fp"]
	sp.HP, sp.Mana = 3, 0
	s.playersMu.Unlock()

	for i := 0; i < 32; i++ {
		s.tick(i)
	}
	got, _ := s.State("fp")
	if got.HP <= 3 || got.Mana <= 0 {
		t.Fatalf("regen broken: %+v", got)
	}
}
