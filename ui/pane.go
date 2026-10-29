package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

// Pane is a titled, bordered box drawn at an exact size: the building block of
// a multi-pane dashboard. Unlike Card, which sits inside Rex's own frame, a
// Pane always draws its border, because a dashboard lays several of them side
// by side inside one surface.
//
// The title sits in the top edge with an optional right-aligned context string
// (a count, a branch). The border is Accent when the pane has focus and Border
// otherwise. An optional scroll readout ("↑N ↓M") sits in the bottom edge. Body
// lines are clipped to the inner width and height, or padded up to them, and
// every cell is drawn on the pane's background (Panel by default), so the
// frame has no bare cells.
//
// Pane is a pure render value like Card: no Init, no state. Its With* methods
// return modified copies.
type Pane struct {
	theme   theme.Theme
	profile colorprofile.Profile
	bg      color.Color
	title   string
	context string
	focused bool
	above   int
	below   int
}

// NewPane returns an unfocused pane in theme t on the Panel background.
// profile is the terminal's colour profile; on ANSI16 or lower the solid fill
// is skipped (paint.Fill).
func NewPane(t theme.Theme, profile colorprofile.Profile) Pane {
	return Pane{theme: t, profile: profile, bg: t.Panel}
}

// WithTitle sets the title drawn at the left of the top edge.
func (p Pane) WithTitle(s string) Pane { p.title = s; return p }

// WithContext sets the string right-aligned in the top edge. It is dropped,
// whole, when it does not fit beside the title.
func (p Pane) WithContext(s string) Pane { p.context = s; return p }

// WithFocus sets whether the pane has focus (Accent border) or not (Border).
func (p Pane) WithFocus(focused bool) Pane { p.focused = focused; return p }

// WithScroll sets the scroll readout: how many body lines lie above and below
// the visible window. Both zero hides the readout.
func (p Pane) WithScroll(above, below int) Pane {
	p.above, p.below = max(above, 0), max(below, 0)
	return p
}

// OnSurface draws the pane on the theme's Surface instead of Panel, for a pane
// that sits directly on the page rather than on a raised area.
func (p Pane) OnSurface() Pane { p.bg = p.theme.Surface; return p }

// Render draws body in a w×h box, border included, so lipgloss.Width of the
// result is w and lipgloss.Height is h. A box smaller than 2×2 cannot hold a
// border and is returned as plain filled cells of that size. Body lines wider
// than the inner width are cut (ANSI-aware, no ellipsis, no reflow); lines
// beyond the inner height are dropped from the bottom.
func (p Pane) Render(body string, w, h int) string {
	w, h = max(w, 0), max(h, 0)
	if w == 0 || h == 0 {
		return ""
	}
	t := p.theme
	base := t.On(t.Text, p.bg)
	if w < 2 || h < 2 {
		row := base.Render(strings.Repeat(" ", w))
		return paint.Fill(strings.TrimSuffix(strings.Repeat(row+"\n", h), "\n"), p.bg, p.profile)
	}

	edge := t.On(t.Border, p.bg)
	if p.focused {
		edge = t.On(t.Accent, p.bg)
	}
	b := lipgloss.RoundedBorder()
	inner, innerH := w-2, h-2

	lines := make([]string, 0, h)
	lines = append(lines, p.top(edge, b, inner))
	var src []string
	if body != "" {
		src = strings.Split(body, "\n")
	}
	for i := range innerH {
		cell := ""
		if i < len(src) {
			cell = xansi.Truncate(src[i], inner, "")
		}
		if gap := inner - lipgloss.Width(cell); gap > 0 {
			cell += base.Render(strings.Repeat(" ", gap))
		}
		lines = append(lines, edge.Render(b.Left)+cell+edge.Render(b.Right))
	}
	lines = append(lines, p.bottom(edge, b, inner))
	return paint.Fill(strings.Join(lines, "\n"), p.bg, p.profile)
}

// top draws the top edge: corner, a dash, the title, filler dashes, the
// context, a dash, corner. Each dash and space is a styled cell.
func (p Pane) top(edge lipgloss.Style, b lipgloss.Border, inner int) string {
	t := p.theme
	// Two cells go to the dashes that keep text off the corners.
	avail := inner - 2
	if avail < 1 || (p.title == "" && p.context == "") {
		return edge.Render(b.TopLeft + strings.Repeat(b.Top, inner) + b.TopRight)
	}
	titleStyle := t.On(t.Text, p.bg).Bold(true)
	if p.focused {
		titleStyle = t.On(t.Accent, p.bg).Bold(true)
	}

	left, leftW := "", 0
	if p.title != "" && avail >= 3 {
		label := xansi.Truncate(p.title, avail-2, "…")
		left = titleStyle.Render(" " + label + " ")
		leftW = lipgloss.Width(label) + 2
	}
	right, rightW := "", 0
	if p.context != "" {
		if need := lipgloss.Width(p.context) + 2; leftW+need <= avail {
			right = t.On(t.Muted, p.bg).Render(" " + p.context + " ")
			rightW = need
		}
	}
	fill := avail - leftW - rightW
	return edge.Render(b.TopLeft+b.Top) + left +
		edge.Render(strings.Repeat(b.Top, fill)) + right +
		edge.Render(b.Top+b.TopRight)
}

// bottom draws the bottom edge, with the scroll readout right-aligned when
// there is one and it fits.
func (p Pane) bottom(edge lipgloss.Style, b lipgloss.Border, inner int) string {
	plain := edge.Render(b.BottomLeft + strings.Repeat(b.Bottom, inner) + b.BottomRight)
	if p.above == 0 && p.below == 0 {
		return plain
	}
	readout := fmt.Sprintf(" ↑%d ↓%d ", p.above, p.below)
	rw := lipgloss.Width(readout)
	if rw > inner-2 {
		return plain
	}
	return edge.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner-rw-1)) +
		p.theme.On(p.theme.Muted, p.bg).Render(readout) +
		edge.Render(b.Bottom+b.BottomRight)
}
