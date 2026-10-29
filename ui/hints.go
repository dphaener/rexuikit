package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/theme"
)

// Hint is one key and what it does, as drawn in a HintBar.
type Hint struct {
	Key  string
	Desc string
}

// HintBar is a one-row strip of key/description pairs on the theme's Well,
// with optional status text on its left. It is a pure render value: Render
// returns a string and the bar starts no timer and returns no tea.Cmd.
//
// Every cell of the row, padding and separators included, is drawn through
// Theme.On with Well as the background, so the row has no see-through cells
// inside a Rex layer and needs no fill pass.
type HintBar struct {
	theme  theme.Theme
	status string
}

// NewHintBar returns a hint bar in theme t.
func NewHintBar(t theme.Theme) HintBar { return HintBar{theme: t} }

// WithStatus returns the bar with status shown at its left edge. Status
// outranks hints when space runs short: hints are dropped first, and only a
// status wider than the bar itself is truncated.
func (b HintBar) WithStatus(status string) HintBar {
	b.status = status
	return b
}

const (
	hintPad  = 1 // blank cells at each edge of the bar
	hintGap  = 2 // blank cells between two hints
	hintStat = 2 // blank cells between status and the first hint
)

// Render returns the bar exactly width cells wide. Hints are kept from the
// first, and the trailing ones that do not fit are dropped whole: a hint is
// never cut in half. width below one yields an empty string.
func (b HintBar) Render(hints []Hint, width int) string {
	if width < 1 {
		return ""
	}
	inner := max(width-2*hintPad, 0)
	t := b.theme

	status := b.status
	if lipgloss.Width(status) > inner {
		status = xansi.Truncate(status, inner, "…")
	}

	key := t.On(t.Accent, t.Well).Bold(true)
	desc := t.On(t.Muted, t.Well)
	text := t.On(t.Text, t.Well)
	blank := t.On(t.Text, t.Well)

	used := lipgloss.Width(status)
	var out strings.Builder
	if status != "" {
		out.WriteString(text.Render(status))
	}
	shown := 0
	for _, h := range hints {
		w := lipgloss.Width(h.Key)
		if h.Desc != "" {
			w += 1 + lipgloss.Width(h.Desc)
		}
		lead := hintGap
		switch {
		case shown == 0 && status == "":
			lead = 0
		case shown == 0:
			lead = hintStat
		}
		if used+lead+w > inner {
			break
		}
		out.WriteString(blank.Render(strings.Repeat(" ", lead)))
		out.WriteString(key.Render(h.Key))
		if h.Desc != "" {
			out.WriteString(blank.Render(" "))
			out.WriteString(desc.Render(h.Desc))
		}
		used += lead + w
		shown++
	}

	row := blank.Render(strings.Repeat(" ", min(hintPad, width))) + out.String()
	pad := width - min(hintPad, width) - used
	if pad > 0 {
		row += blank.Render(strings.Repeat(" ", pad))
	}
	return row
}
