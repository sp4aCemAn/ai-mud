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
	ev := s.DrainGMEvents()
	if len(ev) != 2 || ev[1].Kind != "announce" || len(ev[1].Text) != 200 {
		t.Fatalf("cap: %+v", ev)
	}
}

func fmt_fp(i int) string {
	switch i {
	case 0:
		return "fp"
	default:
		return "fp" + string(rune('a'+i))
	}
}
