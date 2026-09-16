package game

import (
	"encoding/json"
	"strings"

	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Slice 5 in-package pins: the town store counter. The dot IS the
// broker — buildTown lifts the row's authored wares onto the storekeep
// dot; the bump opens the PUBLIC store (store-keyed sessions, many
// players browse concurrently); buys run the shared Store.buyFrom.

func storeTownRow(t *testing.T) storage.WorldObject {
	t.Helper()
	return storage.WorldObject{
		ID: 9, Kind: storage.ObjectVillage, Name: "supplyhold",
		HomeX: 50, HomeY: 12, Radius: 4,
		Payload: json.RawMessage(`{
			"items":[{"id":"supplyhold:emberblade","name":"ember blade",
			          "desc":"cuts warm","price":22,"kind":"gear","effect":"atk:+3"}],
			"npcs":[
				{"type":"storekeep","name":"Vex","x":4,"y":3,
				 "wares":["potion:dark","supplyhold:emberblade"]},
				{"type":"storekeep","name":"Empty Marta","x":8,"y":2,
				 "wares":["nowhere:ghostref"]},
				{"type":"villager","name":"Mira","x":6,"y":5}
			]}`),
	}
}

func bumpDot(t *testing.T, s *Server, dx, dy int) {
	t.Helper()
	p := s.players["fp"]
	s.lockedInteract(p, dx, dy)
}

// npcXY reads where buildTown actually landed a roster dot (the sheet
// anchor is only preferred — townFreeSpot may have moved it).
func npcXY(t *testing.T, tc *TownState, name string) (int, int) {
	t.Helper()
	for _, d := range tc.Dots {
		if d.Name == name {
			return d.X, d.Y
		}
	}
	t.Fatalf("roster dot %q missing", name)
	return 0, 0
}

func TestTownStoreAuthoredWares(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	s.registerTown(storeTownRow(t))
	s.enterTown(p, 9)

	tc := s.townOrBuild(9)

	// stand next to Vex wherever buildTown landed him
	vx, vy := npcXY(t, tc, "Vex")
	forceTownLand(tc, vx-1, vy)
	p.Coins = 50
	p.X, p.Y = vx-1, vy
	s.lockedInteract(p, 1, 0)

	sh := s.activeShopFor(p)
	if sh == nil {
		t.Fatalf("the storekeep bump must open the counter")
	}
	if sh.Keeper != "Vex" || sh.Town != "supplyhold" {
		t.Fatalf("counter identity: %+v", sh)
	}
	if len(sh.Lines) != 2 {
		t.Fatalf("authored waresLINE count: %+v", sh.Lines)
	}
	if sh.Lines[1].Ref != "supplyhold:emberblade" || sh.Lines[1].Price != 22 {
		t.Fatalf("local item didn't resolve: %+v", sh.Lines[1])
	}

	// the third line (a gear buy) applies the registry effect
	events := s.buyFromRef(s.state, p, 2)
	if len(events) == 0 {
		t.Fatal("buy produced no line")
	}
	if p.Coins != 28 || p.Atk != startAtk+3 {
		t.Fatalf("gear buy: coins=%d atk=%d", p.Coins, p.Atk)
	}

	// a step away dismisses the counter (the store itself stays put)
	forceTownLand(tc, vx-2, vy)
	if np := s.lockedInteract(p, -1, 0); np.X != vx-2 || np.Y != vy {
		t.Fatalf("walk should have been plain stepping (%d,%d)", np.X, np.Y)
	}
	if s.activeShopFor(p) != nil {
		t.Fatal("walking away must clear the browsing ref")
	}
}

func TestTownStoreEmptyCounterFallsToTalk(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	s.registerTown(storeTownRow(t))
	s.enterTown(s.players["fp"], 9)
	p := s.players["fp"]
	tc := s.townOrBuild(9)
	mx, my := npcXY(t, tc, "Empty Marta")
	forceTownLand(tc, mx-1, my)
	p.X, p.Y = mx-1, my
	s.lockedInteract(p, 1, 0)
	if s.activeShopFor(p) != nil {
		t.Fatal("a storekeep with no resolvable wares must not mount a counter")
	}
	if s.talks["fp"] == nil {
		t.Fatal("the empty storekeep still talks (the canned pool)")
	}
}

func TestTownStorePublicSessions(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("a", "punch")
	s.Join("b", "counter")
	s.registerTown(storeTownRow(t))
	s.enterTown(s.players["a"], 9)
	s.enterTown(s.players["b"], 9)
	pa := s.players["a"]
	pb := s.players["b"]

	tc := s.townOrBuild(9)
	ax, ay := npcXY(t, tc, "Vex")
	forceTownLand(tc, ax-1, ay)
	forceTownLand(tc, ax+1, ay)

	// both stand beside the counter and bump East / West
	pa.Coins = 50
	pb.Coins = 50
	pa.X, pa.Y = ax-1, ay
	pb.X, pb.Y = ax+1, ay
	s.lockedInteract(pa, 1, 0)
	s.lockedInteract(pb, -1, 0)

	if ka, kb := s.storeOf["a"], s.storeOf["b"]; ka == "" || ka != kb {
		t.Fatalf("both should browse the SAME public counter: %q / %q", ka, kb)
	}
	// the store is one object; buying is per-player
	s.buyFromRef(s.state, pa, 1)
	if pa.Coins != 38 || pb.Coins != 50 {
		t.Fatalf("buy is per-fingerprint: a=%d b=%d", pa.Coins, pb.Coins)
	}
	if s.stores[s.storeOf["b"]] == nil {
		t.Fatal("b's mounted store vanished while browsing")
	}
}

func TestTownStoreNamespaceRejected(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	x, y := 50, 12
	// an unprefixed local item must never leave the tools API
	if _, err := s.PlaceObject(ObjectSpec{
		Kind: storage.ObjectVillage, Name: "supplyhold", X: &x, Y: &y, Radius: 4,
		Data: json.RawMessage(`{"items":[{"id":"wardmail","name":"ward mail","price":3,"kind":"gear","effect":"def:+1"}]}`),
	}); err == nil {
		t.Fatal("a non-namespaced local item must be rejected at tool time")
	}
}

// TestTownStorePatchRefreshesCounter: a tool PATCH to the village row
// retires the warm counter — the next bump re-opens the FRESH wares
// (the stale-authored lines never outlive the row).
func TestTownStorePatchRefreshesCounter(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	s.registerTown(storeTownRow(t))
	s.enterTown(p, 9)

	tc := s.townOrBuild(9)
	vx, vy := npcXY(t, tc, "Vex")
	forceTownLand(tc, vx-1, vy)
	forceTownLand(tc, vx+1, vy)
	p.X, p.Y = vx-1, vy
	s.lockedInteract(p, 1, 0)
	if sh := s.activeShopFor(p); sh == nil || len(sh.Lines) != 2 {
		t.Fatalf("initial counter: %+v", s.activeShopFor(p))
	}

	// PATCH the row: the wares shrink to one ref; the stale counter
	// must retire (the store session drops along the warm TownState)
	fresh := storeTownRow(t)
	fresh.Payload = json.RawMessage(`{
		"npcs":[{"type":"storekeep","name":"Vex","x":4,"y":3,
		         "wares":["potion:dark"]}]}`)
	s.registerTown(fresh)
	_ = s.townOrBuild(9) // the fresh builds

	// the old browsing ref is gone; a re-bump mounts the fresh counter
	if sh := s.activeShopFor(p); sh != nil {
		t.Fatalf("the stale counter must have retired: %+v", sh)
	}
	s.Command("fp", "noop", 0)
	if s.activeShopFor(p) != nil {
		t.Fatal("stale browsing ref survived the row refresh")
	}
	p.X, p.Y = vx-1, vy
	s.lockedInteract(p, 1, 0)
	sh := s.activeShopFor(p)
	if sh == nil || len(sh.Lines) != 1 || sh.Lines[0].Ref != "potion:dark" {
		t.Fatalf("the fresh counter must carry 1 line: %+v", sh)
	}
}

// TestTownStoreUnsortableGearVoice: an authored gear effect the
// registry can't carry is refused with the vendor line — the coin
// never quietly vanishes (no silent no-op buys).
func TestTownStoreUnsortableGearRefused(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	p := s.players["fp"]
	row := storeTownRow(t)
	row.Payload = json.RawMessage(`{
		"items":[{"id":"supplyhold:strangecauldron","name":"cauldron",
		          "desc":"largely decorative","price":9,"kind":"gear","effect":"luck:+2"}],
		"npcs":[{"type":"storekeep","name":"Vex","x":4,"y":3,
		         "wares":["supplyhold:strangecauldron"]}]}`)
	s.registerTown(row)
	s.enterTown(p, 9)
	tc := s.townOrBuild(9)
	vx, vy := npcXY(t, tc, "Vex")
	forceTownLand(tc, vx-1, vy)
	p.Coins = 20
	p.X, p.Y = vx-1, vy
	s.lockedInteract(p, 1, 0)
	events := s.buyFromRef(s.state, p, 1)
	if p.Coins != 20 {
		t.Fatalf("the refusal must return the coin: %d", p.Coins)
	}
	if len(events) == 0 || !strings.Contains(events[0], "beyond this counter's smith") {
		t.Fatalf("refusal voice: %v", events)
	}
}

// TestDoorTileRejectsHostileAnchor: an enemy-group placement on a
// town-claimed 町 is a tool-time reject — the enemy dot composites
// OVER the gate glyph (the door renders as an enemy while its tile
// data stays honest), so the surface must never author that shape.
func TestDoorTileRejectsHostileAnchor(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	// a village row paints its own 町 door at the anchor
	s.registerTown(storeTownRow(t)) // supplyhold at (50,12) → gate there
	s.paintGate(storeTownRow(t))    // the register alone is registry-only; paint is the door
	if s.state.tileAt(50, 12) != tileGate {
		t.Fatalf("gate should have painted: %q", s.state.tileAt(50, 12))
	}
	if _, err := s.PlaceObject(ObjectSpec{
		Kind: storage.ObjectEnemyGroup, Name: "door squatter",
		X: intPtr(50), Y: intPtr(12),
		Data: json.RawMessage(`{"count":1,"level":1}`),
	}); err == nil {
		t.Fatal("an enemy anchor on a town-claimed door must reject")
	}
	// a non-door tile still takes the placement (no over-reach)
	if _, err := s.PlaceObject(ObjectSpec{
		Kind: storage.ObjectEnemyGroup, Name: "plain squatter",
		X: intPtr(40), Y: intPtr(12),
		Data: json.RawMessage(`{"count":1,"level":1}`),
	}); err != nil {
		t.Fatalf("plain anchor rejected: %v", err)
	}
}
