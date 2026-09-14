package game

import (
	"math"
	"strings"
)

// Infinite terrain: the world is an unbounded plane of chunks.
// Every tile is a pure function of (world seed, absolute coords) —
// hash noise instead of a rand sequence — so chunk N looks the same
// whenever it's generated, and adjacent chunks agree because heights
// come from one global coarse lattice.
//
// The base board rides on the same pipeline: a bilinear-interpolated
// coarse lattice (one value per 2×2 tiles), the same Gaussian blur
// passes, the same heightGlyph thresholds as the old finite board.

const chunkSize = 32 // tiles per chunk edge
const blurMargin = 4 // the 2-run blur reaches this many tiles out

// hash01 is a deterministic [0,1) value at one coarse lattice point:
// splitmix64 over (seed, gx, gy). No ordering dependence.
func hash01(seed int64, gx, gy int) float64 {
	h := uint64(seed)
	h ^= uint64(gx) * 0x9E3779B97F4A7C15
	h ^= uint64(gy) * 0xC2B2AE3D27D4EB4F
	h ^= h >> 33
	h *= 0xFF51AFD7ED558CCD
	h ^= h >> 33
	h *= 0xC4CEB9FE1A85EC53
	h ^= h >> 33
	return float64(h>>11) / float64(1<<53)
}

// coarseAt samples the half-resolution noise lattice at absolute
// coarse coords (gx, gy): seed + lattice point, nothing else.
func coarseAt(seed int64, gx, gy int) float64 {
	return hash01(seed, gx, gy)
}

// fieldAt is the fine-grained noise at one absolute tile: bilinear
// interpolation over the coarse lattice (one value every 2 tiles).
func fieldAt(seed int64, x, y int) float64 {
	fx, fy := float64(x)/2, float64(y)/2
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	fx -= float64(x0)
	fy -= float64(y0)
	tl := coarseAt(seed, x0, y0)
	tr := coarseAt(seed, x0+1, y0)
	bl := coarseAt(seed, x0, y0+1)
	br := coarseAt(seed, x0+1, y0+1)
	top := tl*(1-fx) + tr*fx
	bottom := bl*(1-fx) + br*fx
	return top*(1-fy) + bottom*fy
}

// blurPass smooths one axis with the shared kernel (the separable
// blur; the old finite board runs the same filter twice).
func blurPass(f [][]float64, axis int) [][]float64 {
	out := make([][]float64, len(f))
	for y := range f {
		out[y] = make([]float64, len(f[0]))
		for x := range f[0] {
			acc := 0.0
			for k, kw := range blurKernel {
				idx := x + k - len(blurKernel)/2
				if axis == 1 {
					idx = y + k - len(blurKernel)/2
				}
				if idx < 0 {
					continue
				}
				if axis == 0 && idx >= len(f[0]) {
					continue
				}
				if axis == 1 && idx >= len(f) {
					continue
				}
				if axis == 0 {
					acc += f[y][idx] * kw
				} else {
					acc += f[idx][x] * kw
				}
			}
			out[y][x] = acc
		}
	}
	return out
}

// floorDiv is negative-aware integer division.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// floorMod is the matching modulo — always in [0,b) for b>0.
func floorMod(a, b int) int {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

// chunkOf maps an absolute tile to its (cx, cy) chunk.
func chunkOf(x, y int) (int, int) {
	return floorDiv(x, chunkSize), floorDiv(y, chunkSize)
}

// chunkKey names one chunk row set in the generation cache.
type chunkKey struct{ cx, cy int }

// genChunk builds one chunk's tile rows: blur the field twice over a
// padded window, trim, threshold with the shared heightGlyph. The
// 2-run of blurPass (h,v,h,v) reaches ±4 tiles, so the margin makes
// neighboring chunks agree along their shared edges. Cached.
func genChunk(seed int64, cache map[chunkKey][]string, cx, cy int) []string {
	key := chunkKey{cx, cy}
	if c, ok := cache[key]; ok {
		return c
	}

	// fine coords covered by this chunk
	x0, y0 := cx*chunkSize, cy*chunkSize

	w, h := chunkSize+2*blurMargin, chunkSize+2*blurMargin
	field := make([][]float64, h)
	for y := range field {
		field[y] = make([]float64, w)
		for x := range field[y] {
			field[y][x] = fieldAt(seed, x0-blurMargin+x, y0-blurMargin+y)
		}
	}

	smooth := blurPass(blurPass(blurPass(blurPass(field, 0), 1), 0), 1)

	rows := make([]string, chunkSize)
	for y := 0; y < chunkSize; y++ {
		row := make([]rune, chunkSize)
		for x := 0; x < chunkSize; x++ {
			row[x] = heightGlyph(smooth[blurMargin+y][blurMargin+x])
		}
		rows[y] = string(row)
	}
	if cache != nil {
		cache[key] = rows
	}
	return rows
}

// genBoard assembles an initial rect (ww×wh anchored at 0,0) from
// chunk generation — the world is infinite from the first bytes; the
// "board" is just the first slice of the plane.
func genBoard(cache map[chunkKey][]string, seed int64, ww, wh int) []string {
	rows := make([]string, wh)
	for y := 0; y < wh; y++ {
		var b strings.Builder
		for x := 0; x < ww; x++ {
			ccx, ccy := chunkOf(x, y)
			chunk := genChunk(seed, cache, ccx, ccy)
			b.WriteRune([]rune(chunk[floorMod(y, chunkSize)])[floorMod(x, chunkSize)])
		}
		rows[y] = b.String()
	}
	return rows
}

// building the first slice of the plane.
