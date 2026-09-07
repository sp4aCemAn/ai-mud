package game

import (
	"log/slog"
	"time"
)

// The playable world, pre-land-generation: a flat bounded field. The
// player is a dot moving inside these bounds; rooms/terrain come later.
const (
	WorldW = 40
	WorldH = 12

	// staleAfter is how long a player survives without being heard from.
	// The tick loop reaps older players — the stand-in for disconnect
	// handling until sessions signal their own teardown.
	staleAfter = 30 * time.Second
)

// Player is the client-side-visible state of one adventurer.
// Identifying key: the SSH key fingerprint ("" = anonymous/guest).
type Player struct {
	Fingerprint string
	Name        string
	Level       int
	HP          int
	MaxHP       int
	Mana        int // the mage stat
	MaxMana     int
	X, Y        int

	lastSeen time.Time
}

// PlayerView is the narrow interface the UI uses to play. Implemented
// by *Server; swappable for a networked client or a fake in tests.
type PlayerView interface {
	// Join registers (or returns) the player for this fingerprint.
	Join(fingerprint, name string) Player
	// State returns a copy of the player's current state.
	State(fingerprint string) (Player, bool)
	// Move applies a delta (clamped to world bounds) and returns the
	// updated state. Returns false if the fingerprint is unknown.
	Move(fingerprint string, dx, dy int) (Player, bool)
}

// Join registers (or returns the existing) player for a fingerprint.
// A new player spawns centered and is given starting stats. Returns a
// copy so callers can't mutate server state.
func (s *Server) Join(fingerprint, name string) Player {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	if p, ok := s.players[fingerprint]; ok {
		p.lastSeen = time.Now()
		return *p
	}

	p := &Player{
		Fingerprint: fingerprint,
		Name:        name,
		Level:       1,
		HP:          10,
		MaxHP:       10,
		Mana:        5,
		MaxMana:     5,
		X:           WorldW / 2,
		Y:           WorldH / 2,
		lastSeen:    time.Now(),
	}
	s.players[fingerprint] = p
	slog.Info("player joined", "fingerprint", fingerprint, "name", name)
	return *p
}

// Leave removes a player outright (logout / explicit disconnect).
func (s *Server) Leave(fingerprint string) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	delete(s.players, fingerprint)
}

// State implements PlayerView.
func (s *Server) State(fingerprint string) (Player, bool) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	p, ok := s.players[fingerprint]
	if !ok {
		return Player{}, false
	}
	p.lastSeen = time.Now()
	return *p, true
}

// Move implements PlayerView: position deltas are clamped to the world.
func (s *Server) Move(fingerprint string, dx, dy int) (Player, bool) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	p, ok := s.players[fingerprint]
	if !ok {
		return Player{}, false
	}

	p.X = clamp(p.X+dx, 0, WorldW-1)
	p.Y = clamp(p.Y+dy, 0, WorldH-1)
	p.lastSeen = time.Now()

	out := *p
	return out, true
}

// reapStale drops players that haven't been heard from recently. Called
// from the tick loop; the stand-in for disconnect detection.
func (s *Server) reapStale(now time.Time) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	for fp, p := range s.players {
		if now.Sub(p.lastSeen) > staleAfter {
			slog.Info("reaping stale player", "fingerprint", fp, "name", p.Name)
			delete(s.players, fp)
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
