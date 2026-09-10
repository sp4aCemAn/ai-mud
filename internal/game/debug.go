package game

import "strings"

// flatten normalizes the world into an empty walkable field so
// positional tests don't depend on terrain RNG.
func flatten(s *Server) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	w := s.state
	w.ww, w.wh = WorldW, WorldH
	w.enemies = map[int]*Enemy{}
	w.npcDot = Dot{X: -1, Y: -1}
	w.tiles = nil
	for y := 0; y < w.wh; y++ {
		w.tiles = append(w.tiles, strings.Repeat(",", w.ww))
	}
	w.spawnX, w.spawnY = WorldW/2, WorldH/2
	// players too — some joined before this call (route A joins early)
	for _, p := range s.players {
		p.X, p.Y = w.spawnX, w.spawnY
	}
}

// DebugFlattenWorld is the exported test hook for cross-package tests
// (ui drives a real server): empty walkable field, no dots, centered
// spawn. Gameplay never calls it.
func (s *Server) DebugFlattenWorld() { flatten(s) }
