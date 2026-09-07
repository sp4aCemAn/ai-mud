package game

import (
	"testing"
	"time"
)

func TestJoinIdempotentPerFingerprint(t *testing.T) {
	s := NewServer()

	p1 := s.Join("SHA256:abc", "guest")
	p2 := s.Join("SHA256:abc", "guest")
	if p1 == p2 {
		t.Fatal("Join should return copies, not the internal pointer")
	}
	if p1.X != p2.X || p1.Y != p2.Y || p1.Level != p2.Level {
		t.Fatalf("same fingerprint should resolve to same player: %+v vs %+v", p1, p2)
	}
	if p1.X != WorldW/2 || p1.Y != WorldH/2 {
		t.Fatalf("new player should spawn centered, got (%d,%d)", p1.X, p1.Y)
	}
	if p1.HP != p1.MaxHP || p1.Mana != p1.MaxMana || p1.Level != 1 {
		t.Fatalf("starting stats wrong: %+v", p1)
	}
}

func TestMoveClampsToWorldBounds(t *testing.T) {
	s := NewServer()
	s.Join("fp", "guest")

	// walk far past every edge
	for i := 0; i < WorldW+10; i++ {
		if _, ok := s.Move("fp", 1, 0); !ok {
			t.Fatal("move on known player should succeed")
		}
	}
	p, _ := s.State("fp")
	if p.X != WorldW-1 {
		t.Fatalf("east clamp failed: X=%d want %d", p.X, WorldW-1)
	}
	for i := 0; i < WorldH+10; i++ {
		s.Move("fp", 0, -1)
	}
	p, _ = s.State("fp")
	if p.Y != 0 {
		t.Fatalf("north clamp failed: Y=%d", p.Y)
	}
}

func TestMoveUnknownPlayer(t *testing.T) {
	s := NewServer()
	if _, ok := s.Move("nobody", 1, 0); ok {
		t.Fatal("moving an unknown player should fail")
	}
	if _, ok := s.State("nobody"); ok {
		t.Fatal("state of unknown player should fail")
	}
}

func TestStaleReaper(t *testing.T) {
	s := NewServer()
	s.Join("gone", "old")
	s.Join("here", "fresh")

	// age only the first player past the stale window
	s.playersMu.Lock()
	s.players["gone"].lastSeen = time.Now().Add(-staleAfter - time.Second)
	s.playersMu.Unlock()

	s.reapStale(time.Now())

	if _, ok := s.State("gone"); ok {
		t.Fatal("stale player should be reaped")
	}
	if _, ok := s.State("here"); !ok {
		t.Fatal("fresh player should survive the reaper")
	}
}

func TestLeave(t *testing.T) {
	s := NewServer()
	s.Join("fp", "guest")
	s.Leave("fp")
	if _, ok := s.State("fp"); ok {
		t.Fatal("player should be gone after Leave")
	}
}
