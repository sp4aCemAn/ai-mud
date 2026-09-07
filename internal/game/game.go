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

	// players are keyed by fingerprint (the identity for now).
	playersMu sync.Mutex
	players   map[string]*Player

	// TODO: world state — rooms, exits, entities, scheduled events.
	// This is where the AI harness will plug in as the "game master".
}

func NewServer() *Server {
	return &Server{
		sessions:  make(map[string]Session),
		tickEvery: 500 * time.Millisecond,
		players:   make(map[string]*Player),
	}
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
	// TODO(next phase): generate the world here before the tick loop.
	slog.Info("world generation: not implemented — using empty world")

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

func (s *Server) tick(n int) {
	s.reapStale(time.Now())
	// TODO: world simulation goes here (movement, combat, spawns, AI events).
	_ = n
}

func (s *Server) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("game server: %d session(s)", len(s.sessions))
}
