package game

import (
	"math/rand"
)

// The world: one bounded field drawn as ASCII. Terrain comes from a
// coarse noise grid blurred with a separable Gaussian — ' ' renders
// water (impassable), everything else is walkable. All interactive
// dots (enemies, the merchant) live on walkable tiles of the largest
// connected land region, so nothing in the world is unreachable.
//
// Dimensions are dynamic: the world re-generates at the terminal's
// size (Resize), so the playable area fills the visible space.
// WorldW/WorldH are the default size and the test fixture's size.

const (
	// WorldW/WorldH are the default world bounds in tiles — the
	// zoomed-out grid the game plays on until a terminal says otherwise.
	WorldW = 40
	WorldH = 12

	// dim caps keep pathological terminals from exploding the state.
	// Min sizes: beneath these the layout falls apart; the UI gates
	// resize calls, but the server clamps too.
	MinW = 24
	MinH = 8
	MaxW = 120
	MaxH = 60
)

// World is the UI-facing snapshot of the terrain and its dots.
type World struct {
	W, H           int // the field is W×H tiles (dynamic; not the consts)
	Tiles          []string
	Dots           []Dot
	SpawnX, SpawnY int
}

// Dot is one rendered world marker — a group of enemies shares one dot
// in the zoomed-out view; the merchant renders at their own tile.
type Dot struct {
	X, Y  int
	Kind  string // "enemy" | "npc"
	Count int
	Name  string
}

// dim clamps requested world dims to the play range.
func dim(v, lo, hi int) int {
	return clamp(v, lo, hi)
}

// blurKernel is a normalized 1D Gaussian applied horizontally then
// vertically; two passes turn the coarse noise into rolling land.
var blurKernel = []float64{0.05, 0.25, 0.40, 0.25, 0.05}

const (
	// terrain glyphs. Water is the blank space glyph: the field reads
	// as dark land gaps over a lake.
	tileWater  = ' '
	tileShore  = '.'
	tileField  = ','
	tileForest = '·'
)

// blur2D runs the separable blur over a float field.
func blur2D(field [][]float64, kernel []float64) [][]float64 {
	h, w := len(field), len(field[0])
	hor := make([][]float64, h)
	for y := 0; y < h; y++ {
		hor[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			acc := 0.0
			for k, kw := range kernel {
				sx := clamp(x+k-len(kernel)/2, 0, w-1)
				acc += field[y][sx] * kw
			}
			hor[y][x] = acc
		}
	}
	out := make([][]float64, h)
	for y := 0; y < h; y++ {
		out[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			acc := 0.0
			for k, kw := range kernel {
				sy := clamp(y+k-len(kernel)/2, 0, h-1)
				acc += hor[sy][x] * kw
			}
			out[y][x] = acc
		}
	}
	return out
}

// genTerrain blurs a half-resolution noise grid up to world size,
// thresholds it into glyphs, and picks the spawn tile.
func genTerrain(r *rand.Rand, ww, wh int) (tiles []string, spawnX, spawnY int) {
	cw, ch := ww/2+2, wh/2+2
	cg := make([][]float64, ch)
	for y := range cg {
		cg[y] = make([]float64, cw)
		for x := range cg[y] {
			cg[y][x] = r.Float64()
		}
	}
	full := upsample(cg, ww, wh)
	smooth := blur2D(blur2D(full, blurKernel), blurKernel)

	tiles = make([]string, wh)
	for y := 0; y < wh; y++ {
		row := make([]rune, ww)
		for x := 0; x < ww; x++ {
			row[x] = heightGlyph(smooth[y][x])
		}
		tiles[y] = string(row)
	}
	spawnX, spawnY = pickSpawn(tiles, ww, wh)
	return tiles, spawnX, spawnY
}

// upsample stretches src to w×h by nearest-neighbour sampling; the
// blur rounds out the steps.
func upsample(src [][]float64, w, h int) [][]float64 {
	out := make([][]float64, h)
	for y := 0; y < h; y++ {
		out[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			out[y][x] = src[y*len(src)/h][x*len(src[0])/w]
		}
	}
	return out
}

// heightGlyph thresholds the blurred heightmap. Water claims the low
// end; retune the cut points here when balancing.
func heightGlyph(h float64) rune {
	switch {
	case h < 0.42:
		return tileWater
	case h < 0.47:
		return tileShore
	case h < 0.72:
		return tileField
	default:
		return tileForest
	}
}

func walkable(tiles []string, x, y int) bool {
	if x < 0 || y < 0 || y >= len(tiles) || x >= len([]rune(tiles[y])) {
		return false
	}
	return []rune(tiles[y])[x] != tileWater
}

// mainRegion returns the largest connected land region's tiles — the
// no-dead-ends guarantee: spawn, merchant and enemies all place on it.
func mainRegion(tiles []string) [][2]int {
	hh, ww := len(tiles), len([]rune(tiles[0]))
	seen := make([][]bool, hh)
	for y := range tiles {
		seen[y] = make([]bool, ww)
	}
	best := [][2]int(nil)
	for y := 0; y < hh; y++ {
		for x := 0; x < ww; x++ {
			if !walkable(tiles, x, y) || seen[y][x] {
				continue
			}
			var cells [][2]int
			frontier := [][2]int{{x, y}}
			for len(frontier) > 0 {
				c := frontier[len(frontier)-1]
				frontier = frontier[:len(frontier)-1]
				cx, cy := c[0], c[1]
				if !walkable(tiles, cx, cy) || seen[cy][cx] {
					continue
				}
				seen[cy][cx] = true
				cells = append(cells, c)
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					frontier = append(frontier, [2]int{cx + d[0], cy + d[1]})
				}
			}
			if len(cells) > len(best) {
				best = cells
			}
		}
	}
	return best
}

// pickSpawn returns the main-region tile nearest its centroid.
func pickSpawn(tiles []string, _, _ int) (int, int) {
	cells := mainRegion(tiles)
	if len(cells) == 0 {
		// degenerate all-water world: any tile, walkability fallback
		return 0, 0
	}

	// the region member nearest its centroid
	cx, cy := 0, 0
	for _, c := range cells {
		cx += c[0]
		cy += c[1]
	}
	cx, cy = cx/len(cells), cy/len(cells)
	sx, sy, bestd := cells[0][0], cells[0][1], 1<<30
	for _, c := range cells {
		if d := abs(c[0]-cx) + abs(c[1]-cy); d < bestd {
			sx, sy, bestd = c[0], c[1], d
		}
	}
	return sx, sy
}

// randomWalkable picks a walkable tile at least dist steps from
// (sx, sy); 200 tries then relaxes the distance.
func randomWalkable(tiles []string, r *rand.Rand, sx, sy, dist int) (int, int) {
	ww := len([]rune(tiles[0]))
	wh := len(tiles)
	for d := dist; d >= 0; d-- {
		for tries := 0; tries < 200; tries++ {
			x, y := r.Intn(ww), r.Intn(wh)
			if walkable(tiles, x, y) && abs(x-sx)+abs(y-sy) >= d {
				return x, y
			}
		}
	}
	return sx, sy
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
