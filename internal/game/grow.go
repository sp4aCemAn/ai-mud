package game

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

// absorbInto absorbs the absolute tile (nx, ny) into the world grid:
// whole chunks per direction, every absolute coordinate shifts together,
// and authored terrain edits re-apply post-regen (the world stays honest).
func (s *Server) absorbInto(nx, ny int) {
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

	// rebuild: every chunk in the new rect regenerates from the seed —
	// deterministic, so the tiles you've walked are unchanged
	rows := make([]string, bottom-top)
	for y := top; y < bottom; y++ {
		var b strings.Builder
		for x := left; x < right; x++ {
			ccx, ccy := chunkOf(x, y)
			chunk := genChunk(w.seed, w.chunks, ccx, ccy)
			b.WriteRune([]rune(chunk[floorMod(y, chunkSize)])[floorMod(x, chunkSize)])
		}
		rows[y-top] = b.String()
	}

	shiftCols, shiftRows := left-w.wx0, top-w.wy0
	w.tiles = rows
	w.wx0, w.wy0 = left, top
	w.ww, w.wh = right-left, bottom-top

	// shift what's addressed in absolute coords
	w.spawnX += shiftCols
	w.spawnY += shiftRows
	w.npcDot.X += shiftCols
	w.npcDot.Y += shiftRows
	for i := range w.region {
		w.region[i][0] += shiftCols
		w.region[i][1] += shiftRows
	}
	for _, e := range w.enemies {
		e.X += shiftCols
		e.Y += shiftRows
	}
	for _, p := range s.players {
		p.X += shiftCols
		p.Y += shiftRows
	}
	// authored terrain edits survive the regen: re-apply on top
	if s.loaded != nil {
		for _, o := range s.objects {
			if o.Kind == storage.ObjectEdit {
				s.applyTerrainEdit(o)
			}
		}
	}
	w.region = mainRegion(w.tiles)
	w.changed()
}
