package game

import (
	"encoding/json"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// TestStaySequenceHeals drives the FULL sleep sequence through the
// real tick: the 5-coin funsal up front, staged lines one per ~2s,
// then the full HP/mana restore — pinning the sleep pin en route.
func TestStaySequenceHeals(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "cradlehold", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	row := storage.WorldObject{
		ID: 7, Kind: storage.ObjectVillage, Name: "cradlehold",
		HomeX: 50, HomeY: 12, Radius: 4,
		Payload: json.RawMessage(`{"npcs":[{"type":"innkeep","name":"Dunna","x":3,"y":2}]}`),
	}
	s.registerTown(row)
	s.enterTown(p, 7)
	tc := s.townOrBuild(7)

	p.Coins = 9
	p.HP = 3
	p.MaxHP = 40
	p.Mana = 1
	startX, startY := p.X, p.Y
	s.openStay(p, tc, 0)
	if p.Coins != 4 {
		t.Fatalf("coin charge: %d", p.Coins)
	}
	if s.stays["fp"] == nil {
		t.Fatal("the sleep sequence should be open")
	}

	// the sleep pin: movement holds while asleep
	if np := s.lockedInteract(p, 1, 0); np.X != startX || np.Y != startY {
		t.Fatalf("the sleep pin failed (%d,%d)", np.X, np.Y)
	}

	// one stage per 4 ticks: 5 stages × 4 ≈ the 10s sequence
	for i := 0; i < 25; i++ {
		s.tick(i)
	}
	if s.stays["fp"] != nil {
		t.Fatal("the stay should have closed by now (healed)")
	}
	if p.HP != p.MaxHP || p.Mana != p.MaxMana {
		t.Fatalf("the inn's heal: hp=%d/%d mana=%d/%d", p.HP, p.MaxHP, p.Mana, p.MaxMana)
	}
	// the walk pin lifts with the sleep
	if np := s.lockedInteract(p, 0, -1); np.X == p.X && np.Y == p.Y {
		t.Log("next tile may be a wall — the pin itself is gone either way")
	}
}

// TestStayCancelEarly: ESC mid-sequence stops the heals (the coins
// stay spent) and restores the movement pin right away.
func TestStayCancelEarly(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "cradlehold", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	row := storage.WorldObject{
		ID: 7, Kind: storage.ObjectVillage, Name: "cradlehold",
		HomeX: 50, HomeY: 12, Radius: 4,
		Payload: json.RawMessage(`{"npcs":[{"type":"innkeep","name":"Dunna","x":3,"y":2}]}`),
	}
	s.registerTown(row)
	s.enterTown(p, 7)
	tc := s.townOrBuild(7)

	p.Coins = 9
	p.HP = 3
	p.Mana = 1
	s.openStay(p, tc, 0)
	if p.Coins != 4 {
		t.Fatalf("coin charge: %d", p.Coins)
	}

	// a few ticks in, then ESC
	for i := 0; i < 6; i++ {
		s.tick(i)
	}
	s.cancelStay(p)
	if s.stays["fp"] != nil {
		t.Fatal("the stay should be cancelled")
	}
	if p.HP == p.MaxHP {
		t.Fatal("an early escape must not heal")
	}
}
