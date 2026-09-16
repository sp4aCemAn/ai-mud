package game

import (
	"fmt"
	"testing"
	"time"
)

// The GM seam pins (harness slice 1). Observe must NEVER double-take
// playersMu — non-reentrant locks freeze the whole harness goroutine,
// and a freeze looks like "no cycles, no logs, no crash" in prod.

func TestGMRoundupRing(t *testing.T) {
	s := NewServer()
	flatten(s)
	for i := 0; i < gmEventCap+10; i++ { // overflow drains the oldest
		s.Join(fmt.Sprintf("fp%02d", i), "walker")
	}
	events := s.DrainGMEvents()
	if len(events) != gmEventCap {
		t.Fatalf("ring cap: %d (want %d)", len(events), gmEventCap)
	}
	s.Join("fp2", "last")
	events = s.DrainGMEvents()
	if len(events) != 1 || events[0].Kind != "login" || events[0].Text != "last enters the world" {
		t.Fatalf("drain: %+v", events)
	}
	if again := s.DrainGMEvents(); len(again) != 0 {
		t.Fatalf("drain must clear the ring: %+v", again)
	}
}

func TestGMObserveDoesNotDeadlock(t *testing.T) {
	s := NewServer()
	flatten(s)
	s.Join("fp", "Rae")
	done := make(chan GMReadout)
	go func() { done <- s.Observe() }()

	select {
	case <-done: // observe returned: no self-deadlock on playersMu
	case <-time.After(2 * time.Second):
		t.Fatal("Observe deadlocked (double playersMu)")
	}
	// the readout itself: summary + towns sane
	out := s.Observe()
	if out.World.W <= 0 {
		t.Fatalf("summary: %+v", out.World)
	}
	if len(out.Towns) != 0 {
		t.Fatalf("a seed world has no towns: %+v", out.Towns)
	}
	if again := s.Observe(); len(again.Events) != 0 {
		t.Fatalf("the drained events should stay drained: %+v", again.Events)
	}
}

func TestGMTownsCards(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	s.registerTown(storeTownRow(t))
	_ = s.townOrBuild(9) // warm the roster

	towns := s.Towns()
	if len(towns) != 1 {
		t.Fatalf("towns: %+v", towns)
	}
	card := towns[0]
	if card.ID != 9 || card.Name != "supplyhold" || card.Dots != 3 {
		t.Fatalf("town card: %+v", card)
	}
	for _, want := range []string{"storekeep", "villager"} {
		found := false
		for _, have := range card.Roles {
			if have == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("card missing role %q: %+v", want, card.Roles)
		}
	}
}

func TestGMAnnounceCap(t *testing.T) {
	s := NewServer()
	flatten(s)
	s.Join("fp", "guest")
	if err := s.GMAnnounce("   "); err == nil {
		t.Fatal("an empty announce must refuse")
	}
	long := make([]rune, 250)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.GMAnnounce(string(long)); err != nil {
		t.Fatalf("announced cap: %v", err)
	}
	// the feedback rule: announces do NOT note the GM's ring (the GM
	// must never react to its own voice — the runaway cycle once
	// shipped); players felt the line instead (lastEvents fanned out)
	if ev := s.DrainGMEvents(); len(ev) != 1 {
		t.Fatalf("announces ride the fan-out, not the ring: %+v", ev)
	}
	if got := len(s.state.lastEvents["fp"]); got != 1 {
		t.Fatalf("the capped line lands on the player's log: %d", got)
	}
}

func TestFrontierChunkVisitWakesGM(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")

	// boot rect chunks start settled; fresh country beyond is frontier
	s.playersMu.Lock()
	got := s.state.settled[chunkKey{1, 0}]
	s.playersMu.Unlock()
	if got != settleCap {
		t.Fatalf("boot rect should start settled: got %d", got)
	}

	// a first visit to a FRESH chunk (past the boot rect's chunks)
	// raises one explore event + a poke
	s.playersMu.Lock()
	cx, cy := chunkOf(chunkSize*2+5, 10)
	s.chunkVisit(chunkSize*2+5, 10)
	key := chunkKey{cx, cy}
	_, had := s.state.settled[key]
	s.playersMu.Unlock()
	if !had {
		t.Fatalf("the visit must register the fresh chunk")
	}
	events := s.DrainGMEvents()
	found := false
	for _, e := range events {
		if e.Kind == "explore" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the first chunk visit must trigger the explore event: %+v", events)
	}
	select {
	case <-s.Poke():
		// the door woke — good
	default:
		t.Fatal("the frontier must poke the harness door on first visit")
	}

	// a revisit is quiet (the frontier only fires once per chunk)
	s.playersMu.Lock()
	s.chunkVisit(chunkSize*2+6, 10)
	s.playersMu.Unlock()
	if ev := s.DrainGMEvents(); len(ev) != 0 {
		t.Fatalf("a revisit must stay silent: %+v", ev)
	}
	select {
	case <-s.Poke():
		t.Fatal("a revisit must not re-poke the door")
	default:
	}
}

func TestFrontierSpotsRidesTheDeck(t *testing.T) {
	s := NewServerWorld(WorldSpec{Name: "supplyworld", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")

	// settle the boot rect happens at NewServerWorld: the frontier is
	// one ring outward; the mediator must GROW the plane a chunk east
	// (the rect is all-settled) and hand back a walkable dark-edge spot
	s.playersMu.Lock()
	spots := s.frontierSpots(8)
	s.playersMu.Unlock()
	if len(spots) != 0 {
		t.Fatalf("inside an all-settled boot rect there is no frontier: %+v", spots)
	}
	x, y, ok := s.GMFrontierSpot() // takes the lock itself
	if !ok {
		t.Fatal("the mediator must grow the plane and find a frontier spot")
	}
	scx, _ := chunkOf(x, y)
	if scx < 2 { // chunk 0 and 1 are the boot rect's — frontier lies beyond
		t.Fatalf("a spot inside the settled boot chunks: %v", [2]int{x, y})
	}
	if !s.state.walkableAt(x, y) {
		t.Fatal("frontier spots must be walkable")
	}

	// settled rejections ride the same ledger: a spawn in the boot rect
	// bumps its count (the authoring surface stays admin-tools-open)
	s.playersMu.Lock()
	s.state.settled[chunkKey{0, 0}] = settleCap - 1
	s.bumpSettled(2, 2)
	n := s.state.settled[chunkKey{0, 0}]
	s.playersMu.Unlock()
	if n != settleCap {
		t.Fatalf("the bump must count toward settling: %d", n)
	}
}
