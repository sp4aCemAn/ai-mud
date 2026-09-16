package game

import "fmt"

// The frontier population machinery (cactus-integration slice): the
// world tracks WHICH chunks have been visited and how populated they
// are. The GM's queue is FRONTIER-driven — a turn rides on fresh
// country (the player pushes past the dark edge), and a settled
// (>=cap) chunk is quiet ground: the spawn rails prefer the
// settled/unexplored border, so enemies stay at the edge of the map.

// settleCap: the population degree — how many authored entities make
// a chunk "settled" enough that the GM stops generating there.
const settleCap = 3

// chunkVisit records a player's FIRST entry into a chunk: the "explore"
// event rides the harness's Poke door (playersMu held).
func (s *Server) chunkVisit(x, y int) {
	w := s.state
	if w.settled == nil {
		w.settled = map[chunkKey]int{}
	}
	cx, cy := chunkOf(x, y)
	key := chunkKey{cx, cy}
	if _, seen := w.settled[key]; seen {
		return
	}
	w.settled[key] = 0
	s.gmNote("explore", fmt.Sprintf("a wanderer pushed into fresh country (chunk %d,%d)", cx, cy))
}

// bumpSettled counts one authored entity landing in a chunk (playersMu
// held) — the population-degree rule's ledger.
func (s *Server) bumpSettled(x, y int) {
	w := s.state
	if w.settled == nil {
		w.settled = map[chunkKey]int{}
	}
	cx, cy := chunkOf(x, y)
	key := chunkKey{cx, cy}
	if _, ok := w.settled[key]; !ok {
		return // unexplored country can't be "settled" by a stray spawn
	}
	w.settled[key]++
}

// chunkSettled: is this abs tile's chunk already at/over the rail cap?
func (s *Server) chunkSettled(x, y int) bool {
	w := s.state
	if w.settled == nil {
		return false // a young world is all frontier
	}
	cx, cy := chunkOf(x, y)
	return w.settled[chunkKey{cx, cy}] >= settleCap
}

// frontierNeighbor: chunk k touches a settled chunk in its 8-ring
// (playersMu held). The frontier ring is where new content lives.
func frontierNeighbor(w *worldState, k chunkKey) bool {
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			if dx == 0 && dy == 0 {
				continue
			}
			if n, ok := w.settled[chunkKey{k.cx + dx, k.cy + dy}]; ok && n >= settleCap {
				return true
			}
		}
	}
	return false
}

// frontierSpots returns walkable ABS tiles whose chunk is unsettled but
// chunk-adjacent to a settled one — the spawn mediation's pool, ranked
// NEAREST the player's current edge (the dark the explorer is pushing
// into; a random tie-break keeps two turns from stacking one tile).
// playersMu held.
func (s *Server) frontierSpots(limit int) [][2]int {
	w := s.state
	var cand [][2]int
	for y := w.wy0; y < w.wy0+w.wh; y++ {
		for x := w.wx0; x < w.wx0+w.ww; x++ {
			cx, cy := chunkOf(x, y)
			k := chunkKey{cx, cy}
			if n, ok := w.settled[k]; ok && n >= settleCap {
				continue // settled: not frontier
			}
			if !frontierNeighbor(w, k) {
				continue // dark, but not touching the settled edge
			}
			if !w.walkableAt(x, y) {
				continue
			}
			cand = append(cand, [2]int{x, y})
		}
	}
	if len(cand) == 0 {
		return nil
	}
	// SPREAD rule: an anchor near OTHER enemy dots is out of the pool —
	// the "same spot" stacking pattern (two turns, one tile): bands
	// meet the wanderer at DIFFERENT dark-edge tiles, not the nearest
	// rank every time. Truly crowded frontier still earns content.
	clear := func(p [2]int) bool {
		for _, e := range w.enemies {
			if e.HP > 0 && abs(e.X-p[0])+abs(e.Y-p[1]) < 6 {
				return false
			}
		}
		return true
	}
	var spread [][2]int
	for _, p := range cand {
		if clear(p) {
			spread = append(spread, p)
		}
	}
	if len(spread) == 0 {
		spread = cand
	}
	cand = spread
	// rank by walk distance to the nearest live player (the frontier
	// the PLAYER sees) — spread-but-close beats random-anywhere
	player, ok := s.playerFrontier()
	if !ok {
		player = &Player{X: w.spawnX, Y: w.spawnY}
	}
	dist := func(p [2]int) int {
		return abs(p[0]-player.X) + abs(p[1]-player.Y)
	}
	insertionSortBy(cand, dist)
	if len(cand) > limit {
		cand = cand[:limit]
	}
	return cand
}

func insertionSortBy(v [][2]int, dist func([2]int) int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && dist(v[j]) < dist(v[j-1]); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// absNearServed: the anchor is inside the served rect OR within one
// chunk of its edge — the legal tool-anchor bounds on the infinite
// plane (far-off points reject instead of absorbing galaxy distance).
func (w *worldState) absNearServed(x, y int) bool {
	return x >= w.wx0-chunkSize && x < w.wx0+w.ww+chunkSize &&
		y >= w.wy0-chunkSize && y < w.wy0+w.wh+chunkSize
}

// GMFrontierSpot resolves one walkable tile on the settled frontier
// (the GM spawn mediation's anchor): it grows the served rect a chunk
// east when the settled boot rect has no unsettled neighbor inside it.
// The world NEVER gets content inside known country — the frontier rule.
func (s *Server) GMFrontierSpot() (int, int, bool) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	if spots := s.frontierSpots(1); len(spots) > 0 {
		x, y := spots[0][0], spots[0][1]
		return x, y, true
	}
	// all frontier dark: grow one chunk east of the served rect (the
	// absorb machinery re-applies gates/edits and keeps ABS coords)
	w := s.state
	s.absorbInto(w.wx0+w.ww+chunkSize-1, w.wy0+w.wh-1, 0, 0)
	if spots := s.frontierSpots(1); len(spots) > 0 {
		return spots[0][0], spots[0][1], true
	}
	return 0, 0, false
}
