// Package game is the MUD game server.
//
// It owns the world state (rooms, players, entities) and the game tick
// loop. Transport layers (SSH compositor, web API, and eventually the AI
// harness) talk to it through the Session/Command interfaces below so the
// world logic never depends on how a player is connected.
package game

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// Session is the write-end of a connected player (SSH, web, etc).
type Session interface {
	ID() string
	Write([]byte) (int, error)
	Close() error
}

// Server holds all world state.
type Server struct {
	mu        sync.RWMutex
	sessions  map[string]Session
	tickEvery time.Duration

	// state is the world: terrain, dots, fights, stores, event logs.
	state *worldState
	// regenCount folds into world seeds so resized worlds stay
	// reproducible when W_SEED is pinned.
	regenCount int64

	// players are keyed by fingerprint (the identity for now).
	playersMu sync.Mutex
	players   map[string]*Player

	// loaded is the persisted-world spec (nil = classic seed-env flow).
	loaded *WorldSpec

	// wstore is the tool-write path to world_objects (nil = memory mode).
	wstore WorldStore

	// nextEphemeralID numbers placements when no store is attached
	// (memory mode): negative, so they never collide with real rows.
	nextEphemeral int64

	// objects is the tool-authored placements this server owns: the
	// persisted world's rows at boot plus every tool-placed row since.
	objects []storage.WorldObject

	// towns: nested-room spaces lazily built from authored village
	// rows (townRows is the registry seeded at boot replay; TownState
	// builds on first entry and stays warm).
	townRows map[int64]storage.WorldObject
	towns    map[int64]*TownState

	// talks: per-fingerprint open conversation sessions (slice 3)
	talks map[string]*Talk

	// narr: the docdb conversation layer (nil = payload + canned only)
	narr NarrStore

	// stays: per-fingerprint sleep sequences (slice 4)
	stays map[string]*Stay
}

func NewServer() *Server {
	seed := worldSeed()
	s := &Server{
		sessions:  make(map[string]Session),
		tickEvery: 500 * time.Millisecond,
		players:   make(map[string]*Player),
	}
	spawnWorld(s, seed, WorldW, WorldH, 0)
	s.landSpawnRing()
	slog.Info("world generated", "size", fmt.Sprintf("%dx%d", WorldW, WorldH),
		"spawn", fmt.Sprintf("%d,%d", s.state.spawnX, s.state.spawnY),
		"seed", seed)
	return s
}

// regenWorld is retired by the infinite plane: terrain never rebuilds —
// the world grows chunk-wise when players roam past the served rect.
// The call remains as a grow-hint for legacy resize flows.
func (s *Server) regenWorld(pw, ph int) {
	ww, wh := dim(pw, MinW, MaxW), dim(ph, MinH, MaxH)
	if s.state != nil && s.state.ww == ww && s.state.wh == wh {
		return // no-op: same size, keep the world stable
	}

	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	// the world GROWS to fit a bigger board and never shrinks
	w := s.state
	wWW, wWH := w.ww, w.wh
	newW, newH := max(ww, wWW), max(wh, wWH)
	if newW == wWW && newH == wWH {
		return
	}
	versionBefore := w.version
	s.absorbInto(newW-1, newH-1, 0, 0)
	s.replayContentReared(versionBefore)
	s.landSpawnRing()
	// players keep their positions (the world only grew)
	s.state.setEvent("global", "the land ripples — reaching farther, never smaller")
	slog.Info("world grew", "size", fmt.Sprintf("%dx%d", w.ww, w.wh))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// worldSeed is time-based, but W_SEED pins it for reproducible smoke
// tests against a deployed container.
func worldSeed() int64 {
	if v := os.Getenv("W_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return time.Now().UnixNano()
}

// Attach registers a connected client with the game server.
func (s *Server) Attach(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID()] = sess
	slog.Info("session attached", "id", sess.ID())
}

// Detach removes a connected client.
func (s *Server) Detach(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sess.ID())
	slog.Info("session detached", "id", sess.ID())
}

// Broadcast sends a message to every attached session.
func (s *Server) Broadcast(msg []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		if _, err := sess.Write(msg); err != nil {
			slog.Warn("broadcast write failed", "id", sess.ID(), "err", err)
		}
	}
}

// Run is the main game loop: one tick per tickEvery, driving world
// simulation, AI events, and scheduled events. Blocks until ctx done.
func (s *Server) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.tickEvery)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("game server stopping", "err", ctx.Err())
			return nil
		case <-ticker.C:
			s.tick(tick)
			tick++
		}
	}
}

// tick: slow regeneration, enemy respawns. Callable from tests.
func (s *Server) tick(n int) {
	s.reapStale(time.Now())

	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	w.ticks++
	s.advanceStaysLocked()

	// downtime: every 4 s, one HP; every 8 s, one mana.
	for _, p := range s.players {
		if w.ticks%8 == 0 && p.Mana < p.MaxMana {
			p.Mana++
		}
		if w.ticks%4 == 0 && p.HP < p.MaxHP {
			p.HP++
		}
	}

	// dead groups find their way back.
	now := time.Now()
	for _, e := range w.enemies {
		if e.HP <= 0 && !e.respawnAt.IsZero() && now.After(e.respawnAt) {
			x, y := w.randomSpot()
			hp, max := enemyHP(w.rnd, e.Count, e.Level)
			e.X, e.Y = x, y
			e.HP, e.MaxHP = hp, max
			e.respawnAt = time.Time{}
			w.changed()
			slog.Info("enemy group respawns", "name", e.Name)
		}
	}
}

func (s *Server) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("game server: %d session(s)", len(s.sessions))
}
