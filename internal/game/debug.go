package game

import "strings"

// flatten normalizes the world into an empty walkable field so
// positional tests don't depend on terrain RNG. Flat mode stays on:
// absorbed chunk growth (the infinite plane) also lands empty.
func flatten(s *Server) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	w.flat = true
	w.wx0, w.wy0 = 0, 0
	w.ww, w.wh = WorldW, WorldH
	w.enemies = map[int]*Enemy{}
	w.npcDot = Dot{X: -1, Y: -1}
	w.tiles = nil
	for y := 0; y < w.wh; y++ {
		w.tiles = append(w.tiles, strings.Repeat(",", w.ww))
	}
	w.spawnX, w.spawnY = WorldW/2, WorldH/2
	w.region = mainRegion(w.tiles)
	// players too — some joined before this call (route A joins early)
	for _, p := range s.players {
		p.X, p.Y = w.spawnX, w.spawnY
	}
}

// DebugFlattenWorld is the exported test hook for cross-package tests
// (ui drives a real server): empty walkable field, no dots, centered
// spawn. Gameplay never calls it.
func (s *Server) DebugFlattenWorld() { flatten(s) }

// DebugSetPlayer is the cross-package teleport hook: clones p into the
// server's player slot. Gameplay never calls it.
func (s *Server) DebugSetPlayer(fp string, p Player) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	if old := s.players[fp]; old != nil {
		p.lastSeen = old.lastSeen
	}
	s.players[fp] = &p
}

// DebugTownRef exposes the fingerprint's nesting state (cross-package
// assertions; gameplay never calls it).
func (s *Server) DebugTownRef(fp string) *townRef {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	if p := s.players[fp]; p != nil {
		return p.townRef
	}
	return nil
}
