package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

// Tabs is a one-row tab strip: each label is padded by a space either side, the
// active one is drawn bold on Selection and the others Muted on the strip's
// background (Surface by default). Like Pane it is a pure render value with no
// Init; the caller owns which tab is active.
type Tabs struct {
	theme   theme.Theme
	profile colorprofile.Profile
	bg      color.Color
	labels  []string
	active  int
}

// NewTabs returns a strip of labels in theme t with index active selected. An
// out-of-range active selects nothing.
func NewTabs(t theme.Theme, profile colorprofile.Profile, labels []string, active int) Tabs {
	return Tabs{theme: t, profile: profile, bg: t.Surface, labels: labels, active: active}
}

// OnPanel draws the strip on the theme's Panel instead of Surface, for a strip
// that sits inside a Pane.
func (s Tabs) OnPanel() Tabs { s.bg = s.theme.Panel; return s }

// Render draws the strip in exactly w cells and one row. When the labels do
// not fit, leading tabs are replaced by a "…" so the active tab stays visible,
// and what still overflows is cut at the right edge. A w of zero or less
// returns "".
func (s Tabs) Render(w int) string {
	if w <= 0 {
		return ""
	}
	t := s.theme
	idle := t.On(t.Muted, s.bg)
	sel := t.On(t.SelectionText, t.Selection).Bold(true)

	seg := make([]string, len(s.labels))
	widths := make([]int, len(s.labels))
	for i, l := range s.labels {
		text := " " + l + " "
		widths[i] = lipgloss.Width(text)
		if i == s.active {
			seg[i] = sel.Render(text)
		} else {
			seg[i] = idle.Render(text)
		}
	}

	// Drop leading tabs until the active one ends inside the width.
	first, lead := 0, ""
	if s.active >= 0 && s.active < len(seg) {
		for first < s.active {
			total := 0
			for _, x := range widths[first : s.active+1] {
				total += x
			}
			if first > 0 {
				total++ // the "…" marker
			}
			if total <= w {
				break
			}
			first++
		}
		if first > 0 {
			lead = idle.Render("…")
		}
	}
	row := lead + strings.Join(seg[first:], "")
	row = xansi.Truncate(row, w, "")
	if gap := w - lipgloss.Width(row); gap > 0 {
		row += idle.Render(strings.Repeat(" ", gap))
	}
	return paint.Fill(row, s.bg, s.profile)
}
