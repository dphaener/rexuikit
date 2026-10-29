// Package paint makes a rendered frame opaque.
//
// Rex shows the terminal underneath a floating layer through every cell left
// on the default background. lipgloss leaves such cells wherever a styled span
// ends (its SGR reset clears the background), in padding, and in the rest of a
// line, so a frame built from perfectly themed components still has holes.
// Fill closes them after the fact; Holes counts what is left.
package paint

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// Fill returns frame with bg set on every cell that has no background of its
// own. Cells that already carry a background keep it, so wells and accent
// bars survive. The result is rectangular: a short line is padded to the
// widest line's width with bg-coloured cells.
//
// On a 16-colour (or poorer) profile Fill returns frame unchanged: bg would
// be rounded to the nearest of 16 colours, usually black, which makes a
// worse slab than the see-through cells it replaces. This matches forge's
// solidCard.
func Fill(frame string, bg color.Color, profile colorprofile.Profile) string {
	if profile <= colorprofile.ANSI {
		return frame
	}
	w, h := lipgloss.Width(frame), lipgloss.Height(frame)
	canvas := compose(frame, w, h)
	for y := range h {
		for x := range w {
			cell := canvas.CellAt(x, y)
			if !wantsGround(cell) {
				continue
			}
			filled := uv.Cell{Content: " ", Width: 1}
			if cell != nil {
				filled = *cell
			}
			filled.Style.Bg = bg
			canvas.SetCell(x, y, &filled)
		}
	}
	return canvas.Render()
}

// Holes counts the cells of frame, laid out on a w×h canvas, that have no
// background. It counts on the canvas and never on a re-parsed string:
// Canvas.Render trims trailing spaces, so a right-padded line would read as
// shorter than it is. Cells outside the frame but inside w×h count as holes,
// which is the point when asserting a frame covers its whole layer.
func Holes(frame string, w, h int) int {
	canvas := compose(frame, w, h)
	n := 0
	for y := range h {
		for x := range w {
			if wantsGround(canvas.CellAt(x, y)) {
				n++
			}
		}
	}
	return n
}

func compose(frame string, w, h int) *lipgloss.Canvas {
	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewLayer(frame))
	return canvas
}

// wantsGround reports whether a cell is a hole: it exists, it is not the
// continuation of a wide grapheme (which has no style of its own), and
// nothing gave it a background.
func wantsGround(cell *uv.Cell) bool {
	return cell == nil || cell.Width > 0 && cell.Style.Bg == nil
}
