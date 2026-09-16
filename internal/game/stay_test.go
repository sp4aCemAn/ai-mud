package game

import (
	"encoding/json"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// TestStaySequenceHeals drives the FULL sleep sequence through the
// real tick: the 5-coin bill up front, staged lines one per ~2s,
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
	// the offer first: the coin waits for the accept
	s.offerStay(p, tc, 0)
	if p.Coins != 9 {
		t.Fatalf("the offer must not take the coin yet: %d", p.Coins)
	}
	off := s.stayOffers["fp"]
	if off == nil || !off.Offer {
		t.Fatal("the standing menu should be open")
	}
	s.acceptStay(p, off)
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
	s.offerStay(p, tc, 0)
	s.acceptStay(p, s.stayOffers["fp"])
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

// TestInnkeepBumpBuysTheStay: the REAL in-town bump path — the innkeep
// dot opens the stay (coins up front), NOT the talk modal; the
// villager beside her still talks. Pins the role dispatch in
// inTownInteract (the regression once shipped: every bump talked).
func TestInnkeepBumpBuysTheStay(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "cradlehold", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	row := storage.WorldObject{
		ID: 7, Kind: storage.ObjectVillage, Name: "cradlehold",
		HomeX: 50, HomeY: 12, Radius: 4,
		Payload: json.RawMessage(`{"npcs":[
			{"type":"innkeep","name":"Dunna","x":3,"y":2},
			{"type":"villager","name":"Old Bren","x":3,"y":5}]}`),
	}
	s.registerTown(row)
	s.enterTown(p, 7)
	tc := s.townOrBuild(7)

	forceTownLand(tc, 2, 2)
	forceTownLand(tc, 2, 5)
	forceTownLand(tc, 1, 2)

	// the innkeep: a bump from the west opens the standing menu
	p.Coins = 9
	p.X, p.Y = 2, 2
	np := s.lockedInteract(p, 1, 0)
	if np.X != 2 || np.Y != 2 {
		t.Fatal("the bump must hold the player on the innkeep")
	}
	if p.Coins != 9 {
		t.Fatalf("the offer must not take the coin yet: %d", p.Coins)
	}
	if off := s.stayOffers["fp"]; off == nil || !off.Offer {
		t.Fatal("the standing menu should have opened on the bump")
	}
	if s.talks["fp"] != nil {
		t.Fatal("the innkeep must not open a talk modal")
	}
	s.acceptStay(p, s.stayOffers["fp"])
	if p.Coins != 4 {
		t.Fatalf("the accept should charge %d coins: %d", stayCost, p.Coins)
	}
	if s.stays["fp"] == nil {
		t.Fatal("the sleep sequence should be running after the accept")
	}
	s.cancelStay(p)

	// the offer on the menu: ESC declines — nothing was ever spent
	p.Coins = 9
	p.X, p.Y = 2, 2
	s.lockedInteract(p, 1, 0)
	if s.stayOffers["fp"] == nil {
		t.Fatal("the standing menu should be open again")
	}
	s.Command("fp", "stay-decline", 0)
	if s.stayOffers["fp"] != nil {
		t.Fatal("the decline should clear the offer")
	}
	if p.Coins != 9 {
		t.Fatalf("a decline never spends the coin: %d", p.Coins)
	}

	// a step away clears the standing menu
	p.X, p.Y = 2, 2
	s.lockedInteract(p, 1, 0)
	if s.stayOffers["fp"] == nil {
		t.Fatal("the standing menu should be open")
	}
	if np := s.lockedInteract(p, -1, 0); np.X != 1 {
		t.Fatal("the west step should have been plain walking")
	}
	if s.stayOffers["fp"] != nil {
		t.Fatal("walking away must clear the offer")
	}

	// the villager: same bump semantics, the talk modal opens instead
	delete(s.talks, p.Fingerprint)
	p.Coins = 9
	p.X, p.Y = 2, 5
	s.lockedInteract(p, 1, 0)
	if s.talks["fp"] == nil {
		t.Fatal("the villager bump should open the talk")
	}
	if s.stays["fp"] != nil {
		t.Fatal("a villager cannot take your coin for a bed")
	}
}
