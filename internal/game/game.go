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
	"math/rand"
	"sync"
	"time"
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

	// players are keyed by fingerprint (the identity for now).
	playersMu sync.Mutex
	players   map[string]*Player
}

func NewServer() *Server {
	s := &Server{
		sessions:  make(map[string]Session),
		tickEvery: 500 * time.Millisecond,
		players:   make(map[string]*Player),
	}
	spawnWorld(s, rand.New(rand.NewSource(time.Now().UnixNano())))
	slog.Info("world generated", "size", fmt.Sprintf("%dx%d", WorldW, WorldH),
		"spawn", fmt.Sprintf("%d,%d", s.state.spawnX, s.state.spawnY))
	return s
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
			slog.Info("enemy group respawns", "name", e.Name)
		}
	}
}

func (s *Server) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("game server: %d session(s)", len(s.sessions))
}
