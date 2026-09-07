// Package ui — kit.go holds the reusable rendering primitives.
//
// Overlay compositing is ANSI-aware (x/ansi cell cutting), so styled
// backgrounds can be spliced through without breaking escape sequences.
// Components here are meant to be stacked and reused (inventory, trade
// windows, NPC dialogs, …).
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Panel is a bordered box with an optional title — the base for the map
// view, the stats column, floating windows, and future dialogs.
type Panel struct {
	Title   string
	Content string
	Width   int // inner content width; 0 = auto
}

func (p Panel) Render() string {
	var b strings.Builder
	if p.Title != "" {
		b.WriteString(p.Title)
		b.WriteString("\n")
	}
	b.WriteString(p.Content)
	return boxStyle.Render(b.String())
}

// Bar renders a labeled progress bar, e.g. `HP  [████░░░░░] 4/10`.
// Reused for HP, mana, and later XP / trade progress / etc.
func Bar(label string, cur, max, width int) string {
	if width < 1 {
		width = 1
	}
	if cur < 0 {
		cur = 0
	}
	if max < 1 {
		max = 1
	}
	if cur > max {
		cur = max
	}
	filled := cur * width / max
	return label + " [" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// Overlay centers fg over bg, ANSI-aware (x/ansi cell cutting keeps
// escape sequences intact when a line is spliced through).
func Overlay(bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	bgW := maxLineWidth(bgLines)
	fgW := maxLineWidth(fgLines)

	x0 := (bgW - fgW) / 2
	if x0 < 0 {
		x0 = 0
	}
	y0 := (len(bgLines) - len(fgLines)) / 2
	if y0 < 0 {
		y0 = 0
	}

	// grow the background vertically if the overlay is taller
	for len(bgLines) < y0+len(fgLines) {
		bgLines = append(bgLines, "")
	}

	for i, fl := range fgLines {
		y := y0 + i
		bgLine := bgLines[y]
		flW := ansi.StringWidth(fl)

		left := ansi.Cut(bgLine, 0, x0)
		if pad := x0 - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(bgLine, x0+flW, "")
		bgLines[y] = left + fl + right
	}
	return strings.Join(bgLines, "\n")
}

func maxLineWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			w = n
		}
	}
	return w
}

// hint renders a footer hint line, e.g. for key legends. One place so
// every screen formats hints identically.
func hint(s string) string {
	return lipgloss.NewStyle().Faint(true).Render(s)
}
