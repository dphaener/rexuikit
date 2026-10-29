// Package ui is rexuikit's native content kit. Every component renders cells
// that all carry an explicit background, so a Rex floating layer never shows
// the terminal through them.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

// Card padding: one row above the content, two columns either side.
const (
	cardPadRows = 1
	cardPadCols = 2
)

// Card is the frame a form or panel is drawn in.
//
// The default Card has no border and no title of its own. Inside a Rex layer,
// Rex's border and title bar are the frame and the surface's label is the
// title, so the card fills its whole w×h frame edge to edge with the surface
// colour and draws only padding, an optional subtitle, the content and a hint
// bar on the bottom row. Bordered opts into a drawn border and title for a
// host that has no Rex chrome (a plain terminal, a golden test).
//
// Card is a value; its With* and Bordered methods return modified copies.
type Card struct {
	theme    theme.Theme
	profile  colorprofile.Profile
	subtitle string
	hint     string
	bordered bool
	title    string
}

// NewCard returns a borderless card in theme t. profile is the terminal's
// colour profile; on ANSI16 or lower the solid fill is skipped (paint.Fill).
func NewCard(t theme.Theme, profile colorprofile.Profile) Card {
	return Card{theme: t, profile: profile}
}

// WithSubtitle sets a one-line subtitle drawn inside the content area, above
// the content, in the Muted colour.
func (c Card) WithSubtitle(s string) Card { c.subtitle = s; return c }

// WithHint sets the bottom hint bar's text, such as "enter submit · esc cancel".
func (c Card) WithHint(s string) Card { c.hint = s; return c }

// Bordered returns the card with a rounded border in the Border colour and
// title set into the top edge. Use it only where Rex draws no chrome of its
// own.
func (c Card) Bordered(title string) Card { c.bordered = true; c.title = title; return c }

// NaturalSize is the card's size in cells for content: the smallest frame
// that shows the content, subtitle and hint without truncation. Surfaces use
// it to size the Rex layer to the form.
func (c Card) NaturalSize(content string) (cols, rows int) {
	inner := lipgloss.Width(content)
	if c.subtitle != "" {
		inner = max(inner, lipgloss.Width(c.subtitle))
	}
	cols = inner + 2*cardPadCols
	if c.hint != "" {
		// The hint bar is padded by the same columns as the content.
		cols = max(cols, lipgloss.Width(c.hint)+2*cardPadCols)
	}
	rows = cardPadRows + lipgloss.Height(content)
	if content == "" {
		rows = cardPadRows
	}
	if c.subtitle != "" {
		rows += 2 // the subtitle and a blank row under it
	}
	if c.hint != "" {
		rows += 2 // a blank row and the bar
	} else {
		rows += cardPadRows
	}
	if c.bordered {
		cols = max(cols, lipgloss.Width(c.title)+6)
		cols += 2
		rows += 2
	}
	return cols, rows
}

// Render draws content in a w×h frame. A w or h of zero or less means the
// natural size on that axis. Content wider or taller than the frame is cut
// (columns by ANSI-aware truncation, rows from the bottom) rather than
// reflowed, and the hint bar always keeps the last row.
func (c Card) Render(content string, w, h int) string {
	nw, nh := c.NaturalSize(content)
	if w <= 0 {
		w = nw
	}
	if h <= 0 {
		h = nh
	}
	var frame string
	if c.bordered {
		frame = c.border(c.body(content, max(w-2, 1), max(h-2, 1)), w)
	} else {
		frame = c.body(content, w, h)
	}
	return paint.Fill(frame, c.theme.Surface, c.profile)
}

// body lays out the padded content, subtitle and hint bar in w×h cells, every
// row styled Text-on-Surface (or Muted / hint-on-Well) so the only holes left
// are the ones content spans leave, which paint.Fill closes.
func (c Card) body(content string, w, h int) string {
	t := c.theme
	base := t.On(t.Text, t.Surface)
	pad := strings.Repeat(" ", cardPadCols)
	inner := max(w-2*cardPadCols, 0)
	fitted := func(style lipgloss.Style, line string) string {
		// Width pads a short row out to the full frame width in the style's
		// own colours, so no row needs a fill pass to reach the right edge.
		return style.Width(w).Render(pad + xansi.Truncate(line, inner, ""))
	}

	var rows []string
	for range cardPadRows {
		rows = append(rows, fitted(base, ""))
	}
	if c.subtitle != "" {
		rows = append(rows, fitted(t.On(t.Muted, t.Surface), c.subtitle), fitted(base, ""))
	}
	if content != "" {
		for _, line := range strings.Split(content, "\n") {
			rows = append(rows, fitted(base, line))
		}
	}

	if c.hint != "" {
		rows = rows[:min(len(rows), max(h-1, 0))]
		for len(rows) < h-1 {
			rows = append(rows, fitted(base, ""))
		}
		rows = append(rows, fitted(t.On(t.Muted, t.Well), c.hint))
	} else {
		rows = rows[:min(len(rows), h)]
		for len(rows) < h {
			rows = append(rows, fitted(base, ""))
		}
	}
	return strings.Join(rows, "\n")
}

// border wraps body (already w-2 columns wide) in a rounded border with the
// title set into the top edge. Every border cell carries Border on Surface.
func (c Card) border(body string, w int) string {
	t := c.theme
	edge := t.On(t.Border, t.Surface)
	b := lipgloss.RoundedBorder()
	inner := max(w-2, 0)

	top := b.TopLeft + strings.Repeat(b.Top, inner) + b.TopRight
	if c.title != "" && inner >= 6 {
		label := xansi.Truncate(c.title, inner-4, "…")
		rest := inner - lipgloss.Width(label) - 3
		top = edge.Render(b.TopLeft+b.Top+" ") +
			t.On(t.Accent, t.Surface).Bold(true).Render(label) +
			edge.Render(" "+strings.Repeat(b.Top, rest)+b.TopRight)
	} else {
		top = edge.Render(top)
	}

	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines)+2)
	out = append(out, top)
	for _, l := range lines {
		out = append(out, edge.Render(b.Left)+l+edge.Render(b.Right))
	}
	out = append(out, edge.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner)+b.BottomRight))
	return strings.Join(out, "\n")
}
