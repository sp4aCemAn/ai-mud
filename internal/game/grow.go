package game

import (
	"strings"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// absorbInto absorbs the absolute tile (nx, ny) into the world grid:
// growth happens in whole chunks per direction (only the FRESH bands
// are generated — old tiles are copied verbatim), every absolute
// coordinate shifts together when the origin moves, and authored
// terrain edits re-apply after the rebuild (the world stays honest).
func (s *Server) absorbInto(nx, ny, dx, dy int) {
	w := s.state
	if nx >= w.wx0 && nx < w.wx0+w.ww && ny >= w.wy0 && ny < w.wy0+w.wh {
		return // inside; nothing to grow
	}

	left, top := w.wx0, w.wy0
	right, bottom := w.wx0+w.ww, w.wy0+w.wh
	if nx < left {
		left = floorDiv(nx, chunkSize) * chunkSize
	}
	if ny < top {
		top = floorDiv(ny, chunkSize) * chunkSize
	}
	if nx >= right {
		right = (floorDiv(nx, chunkSize) + 1) * chunkSize
	}
	if ny >= bottom {
		bottom = (floorDiv(ny, chunkSize) + 1) * chunkSize
	}

	// build the new rows: fresh chunk bands west/east + the preserved
	// old row in the middle; north/south row bands are all-new rows
	rows := make([]string, bottom-top)
	for y := top; y < bottom; y++ {
		oldY := y - w.wy0 // the row's index in the OLD grid (abs)
		var b strings.Builder
		if oldY < 0 || oldY >= len(w.tiles) {
			// a brand-new row band (north/south): full-width fresh band
			b.WriteString(bandRow(w, y, left, right-1))
		} else {
			if left < w.wx0 { // west band (may span chunks)
				b.WriteString(bandRow(w, y, left, w.wx0-1))
			}
			b.WriteString(w.tiles[oldY])
			if w.wx0+w.ww <= right-1 { // east band
				b.WriteString(bandRow(w, y, w.wx0+w.ww, right-1))
			}
		}
		rows[y-top] = b.String()
	}

	w.tiles = rows
	w.wx0, w.wy0 = left, top
	w.ww, w.wh = right-left, bottom-top

	// absolute coordinates NEVER shift — the rect moves around the
	// world; players, enemies and dots keep their tile positions
	// authored terrain edits survive the rebuild: re-apply on top
	if s.loaded != nil {
		for _, o := range s.objects {
			if o.Kind == storage.ObjectEdit {
				s.applyTerrainEdit(o)
			}
		}
	}
	fresh := mainRegion(w.tiles)
	for i := range fresh { // mainRegion answers LOCAL coords; make ABS
		fresh[i][0] += left
		fresh[i][1] += top
	}
	w.region = fresh

	// guarantee the roam: a landing beach must run through the FRESH
	// band along the step's direction (a fresh band may otherwise be
	// an unbroken ocean wall and pin the player at the seam forever).
	// rows ny±2, cols [left..nx] for west-bound steps (mirror for east);
	// add the north/south rects symmetrically. Anything beyond the
	// guaranteed band is natural-terrain politics.
	guarantee := func(x, y int) {
		lx, ly := x-w.wx0, y-w.wy0
		if ly < 0 || ly >= len(w.tiles) || lx < 0 || lx >= len([]rune(w.tiles[ly])) {
			return
		}
		if row := []rune(w.tiles[ly]); row[lx] == tileWater {
			row[lx] = tileField
			w.tiles[ly] = string(row)
		}
	}
	if dx < 0 { // west-bound: beach cols [left..nx], rows ny±2
		for x := left; x <= nx; x++ {
			for yy := ny - 2; yy <= ny+2; yy++ {
				guarantee(x, yy)
			}
		}
	}
	if dx > 0 { // east-bound: beach cols [nx..right-1]
		for x := nx - 1; x <= right-1; x++ {
			for yy := ny - 2; yy <= ny+2; yy++ {
				guarantee(x, yy)
			}
		}
	}
	if dy < 0 { // north-bound: beach rows [top..ny], cols nx±2
		for yy := top - 1; yy <= ny; yy++ {
			for x := nx - 2; x <= nx+2; x++ {
				guarantee(x, yy)
			}
		}
	}
	if dy > 0 { // south-bound: beach rows [ny..bottom-1]
		for yy := ny - 1; yy <= bottom-1; yy++ {
			for x := nx - 2; x <= nx+2; x++ {
				guarantee(x, yy)
			}
		}
	}

	w.changed()
}

// landSpawnRing guarantees the spawn's 4-neighborhood is land: the
// centroid pick can sit on a 1-tile tongue surrounded by lake (which
// pinned the SSH movement smoke deterministically), and roams need a
// walkable direction out of spawn the moment they connect.
func (s *Server) landSpawnRing() {
	w := s.state
	land := func(x, y int) {
		lx, ly := x-w.wx0, y-w.wy0
		if ly < 0 || ly >= len(w.tiles) || lx < 0 || lx >= len([]rune(w.tiles[ly])) {
			return
		}
		if row := []rune(w.tiles[ly]); row[lx] == tileWater {
			row[lx] = tileField
			w.tiles[ly] = string(row)
		}
	}
	land(w.spawnX, w.spawnY-1)
	land(w.spawnX, w.spawnY+1)
	land(w.spawnX-1, w.spawnY)
	land(w.spawnX+1, w.spawnY)
	w.changed()
}

// bandRow assembles one absolute row's glyph string across [left..right]
// from the chunk generator (all fresh band content).
func bandRow(w *worldState, y, left, right int) string {
	if w.flat { // debug mode: growth lands as empty floor too
		return strings.Repeat(",", right-left+1)
	}
	var b strings.Builder
	for x := left; x <= right; x++ {
		ccx, ccy := chunkOf(x, y)
		chunk := genChunk(w.seed, w.chunks, ccx, ccy)
		b.WriteRune([]rune(chunk[floorMod(y, chunkSize)])[floorMod(x, chunkSize)])
	}
	return b.String()
}
