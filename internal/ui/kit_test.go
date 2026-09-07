package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPanelRendersTitleAndContent(t *testing.T) {
	p := Panel{Title: "stats", Content: "HP 10/10", Width: 10}
	out := p.Render()
	if !strings.Contains(out, "stats") || !strings.Contains(out, "HP 10/10") {
		t.Fatalf("panel missing title/content: %q", out)
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Fatal("panel should have rounded borders")
	}
}

func TestBarProportions(t *testing.T) {
	b := Bar("HP", 10, 10, 10)
	if !strings.Contains(b, "██████████") || strings.Contains(b, "░") {
		t.Fatalf("full bar wrong: %q", b)
	}
	b = Bar("HP", 4, 10, 10)
	if !strings.Contains(b, "████") || !strings.Contains(b, "░░░░░░") {
		t.Fatalf("half bar wrong: %q", b)
	}
	b = Bar("HP", 0, 10, 10)
	if strings.Contains(b, "█") {
		t.Fatalf("empty bar wrong: %q", b)
	}
	// clamping
	b = Bar("HP", 99, 10, 10)
	if !strings.Contains(b, "██████████") {
		t.Fatalf("overfill should clamp: %q", b)
	}
}

func TestOverlayCentersForeground(t *testing.T) {
	bg := strings.Join([]string{
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
	}, "\n")
	fg := strings.Join([]string{
		"+----+",
		"| inv |",
		"+----+",
	}, "\n")

	out := Overlay(bg, fg)
	lines := strings.Split(out, "\n")

	wantH := bgLinesOf(bg)
	if len(lines) != wantH {
		t.Fatalf("overlay must not change bg height: got %d want %d", len(lines), wantH)
	}
	if !strings.Contains(out, "inv") {
		t.Fatal("overlay content missing")
	}
	// centered: the + of row 0 of fg should start around column 2-3
	y := 1 // (5-3)/2
	idx := strings.Index(lines[y], "+----+")
	if idx < 1 || idx > 3 {
		t.Fatalf("overlay not horizontally centered: col=%d", idx)
	}
	// background preserved around the overlay
	if !strings.Contains(lines[4], "....") {
		t.Fatal("background rows should survive")
	}
}

func TestOverlayTallerThanBackground(t *testing.T) {
	bg := "abc"
	fg := "x\ny\nz\nw\nv\nu\n"
	out := Overlay(bg, fg)
	if !strings.Contains(out, "u") {
		t.Fatal("overlay taller than bg must still render fully")
	}
}

func TestOverlayPreservesAnsiInBackground(t *testing.T) {
	// background with a colored span that the overlay splices THROUGH
	bg := "\x1b[31maaaaaaaaaa\x1b[0m\nbbbbbbbbbb\ncccccccccc"
	fg := "XY\nZZ"
	out := Overlay(bg, fg)

	if !strings.Contains(out, "XY") || !strings.Contains(out, "ZZ") {
		t.Fatalf("overlay content missing: %q", out)
	}
	// the lib splices at cell boundaries and re-opens the color after
	// the paste — sequences must stay balanced and well-formed
	if got := strings.Count(out, "\x1b[31m"); got != 2 {
		t.Fatalf("color-open count wrong (%d): %q", got, out)
	}
	if got := strings.Count(out, "\x1b[0m"); got != 2 {
		t.Fatalf("reset count wrong (%d): %q", got, out)
	}
	// visible width of the spliced line is unchanged
	if w := ansi.StringWidth(strings.Split(out, "\n")[0]); w != 10 {
		t.Fatalf("visible width changed: %d", w)
	}
	// line count unchanged
	if got := strings.Count(out, "\n"); got != 2 {
		t.Fatalf("bg height changed: %d", got)
	}
}

func bgLinesOf(s string) int { return len(strings.Split(s, "\n")) }
