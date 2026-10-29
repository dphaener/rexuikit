package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

// styledBody is dashboard-like body text built from spans that each end in an
// SGR reset, the shape that leaves background holes in a naive pane.
func styledBody(th theme.Theme) string {
	return strings.Join([]string{
		th.On(th.Done, th.Panel).Render("● ready") + " plain tail",
		th.On(th.Need, th.Panel).Render("● needs you"),
		"",
		"bare line with no styling at all",
	}, "\n")
}

// TestPaneSizing: the box is exactly w×h for every geometry, including the
// degenerate ones. Load-bearing: it asserts lipgloss.Width and line count
// equal the request, which is what lets a dashboard compose panes by
// arithmetic, and zero holes on every row, the kit's paint rule.
func TestPaneSizing(t *testing.T) {
	th := theme.Dark()
	body := styledBody(th)
	base := NewPane(th, colorprofile.TrueColor)
	tests := []struct {
		name string
		pane Pane
		w, h int
	}{
		{"plain", base, 30, 8},
		{"titled", base.WithTitle("Sessions"), 30, 8},
		{"titled with context", base.WithTitle("Sessions").WithContext("3 of 9"), 30, 8},
		{"focused", base.WithTitle("Sessions").WithFocus(true), 30, 8},
		{"scroll readout", base.WithTitle("Sessions").WithScroll(2, 5), 30, 8},
		{"on surface", base.WithTitle("Sessions").OnSurface(), 30, 8},
		{"title too long", base.WithTitle("A very long pane title indeed").WithContext("ctx"), 14, 5},
		{"context only", base.WithContext("ctx"), 20, 4},
		{"narrow", base.WithTitle("Sessions").WithScroll(1, 1), 5, 4},
		{"minimum", base.WithTitle("x"), 2, 2},
		{"one row", base, 10, 1},
		{"one column", base, 1, 6},
		{"wide", base.WithTitle("Sessions").WithContext("ctx"), 120, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pane.Render(body, tt.w, tt.h)
			if gw, gh := lipgloss.Width(got), lipgloss.Height(got); gw != tt.w || gh != tt.h {
				t.Errorf("rendered %dx%d, want %dx%d:\n%s", gw, gh, tt.w, tt.h, stripANSI(got))
			}
			for i, line := range strings.Split(got, "\n") {
				if lw := lipgloss.Width(line); lw != tt.w {
					t.Errorf("line %d is %d wide, want %d", i, lw, tt.w)
				}
			}
			assertNoHoles(t, got, tt.w, tt.h)
		})
	}
	if got := base.Render(body, 0, 5); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}

// TestPaneClipsAndPads: long lines are cut at the inner width, extra lines are
// dropped from the bottom, and short bodies are padded to the inner height.
func TestPaneClipsAndPads(t *testing.T) {
	th := theme.Dark()
	p := NewPane(th, colorprofile.TrueColor)

	long := stripANSI(p.Render("0123456789ABCDEF", 10, 3))
	if want := "│01234567│"; strings.Split(long, "\n")[1] != want {
		t.Errorf("clipped row = %q, want %q", strings.Split(long, "\n")[1], want)
	}

	tall := stripANSI(p.Render("a\nb\nc\nd\ne", 6, 4))
	if strings.Contains(tall, "c") || !strings.Contains(tall, "b") {
		t.Errorf("body not clipped to the two inner rows:\n%s", tall)
	}

	short := strings.Split(stripANSI(p.Render("a", 6, 5)), "\n")
	if len(short) != 5 || short[1] != "│a   │" || short[3] != "│    │" {
		t.Errorf("short body not padded:\n%s", strings.Join(short, "\n"))
	}

	empty := stripANSI(p.Render("", 6, 3))
	if strings.Split(empty, "\n")[1] != "│    │" {
		t.Errorf("empty body row = %q", strings.Split(empty, "\n")[1])
	}
}

// TestPaneStyledBodyClipsByCells: a span cut mid-way by the clip must not
// leave its colour running or the width short.
func TestPaneStyledBodyClipsByCells(t *testing.T) {
	th := theme.Dark()
	body := th.On(th.Done, th.Panel).Render("● ready and waiting for you")
	got := NewPane(th, colorprofile.TrueColor).Render(body, 12, 3)
	assertNoHoles(t, got, 12, 3)
	if row := strings.Split(stripANSI(got), "\n")[1]; row != "│● ready an│" {
		t.Errorf("styled row = %q", row)
	}
}

// TestPaneTitleRow: the title is on the left, the context right-aligned, and
// the context is dropped whole when it would collide with the title.
func TestPaneTitleRow(t *testing.T) {
	th := theme.Dark()
	p := NewPane(th, colorprofile.TrueColor).WithTitle("Sessions")
	top := func(p Pane, w int) string {
		return strings.Split(stripANSI(p.Render("", w, 3)), "\n")[0]
	}
	if got := top(p, 20); got != "╭─ Sessions ───────╮" {
		t.Errorf("top = %q", got)
	}
	withCtx := top(p.WithContext("3/9"), 24)
	if !strings.HasPrefix(withCtx, "╭─ Sessions ") || !strings.HasSuffix(withCtx, " 3/9 ─╮") {
		t.Errorf("context not right-aligned: %q", withCtx)
	}
	if got := top(p.WithContext("a context far too long"), 18); strings.Contains(got, "context") {
		t.Errorf("colliding context not dropped: %q", got)
	}
	if got := top(p.WithTitle("A very long title"), 12); !strings.Contains(got, "…") || lipgloss.Width(got) != 12 {
		t.Errorf("long title not truncated with an ellipsis: %q", got)
	}
	if got := top(NewPane(th, colorprofile.TrueColor), 8); got != "╭──────╮" {
		t.Errorf("untitled top = %q", got)
	}
}

// TestPaneFocusedBorder: focus switches the border from Border to Accent. The
// bottom edge carries no text, so its colour is purely the border's.
func TestPaneFocusedBorder(t *testing.T) {
	th := theme.Dark()
	bottom := func(focused bool) string {
		lines := strings.Split(NewPane(th, colorprofile.TrueColor).WithFocus(focused).Render("x", 10, 4), "\n")
		return lines[len(lines)-1]
	}
	accent, border := "38;2;137;180;250", "38;2;69;71;90"
	if f := bottom(true); !strings.Contains(f, accent) || strings.Contains(f, border) {
		t.Errorf("focused border is not Accent: %q", f)
	}
	if u := bottom(false); !strings.Contains(u, border) || strings.Contains(u, accent) {
		t.Errorf("unfocused border is not Border: %q", u)
	}
}

// TestPaneScrollReadout: the readout is right-aligned in the bottom edge, is
// hidden at 0/0, and is dropped when the edge is too short for it.
func TestPaneScrollReadout(t *testing.T) {
	th := theme.Dark()
	p := NewPane(th, colorprofile.TrueColor)
	bottom := func(p Pane, w int) string {
		lines := strings.Split(stripANSI(p.Render("x", w, 4)), "\n")
		return lines[len(lines)-1]
	}
	if got := bottom(p.WithScroll(2, 15), 20); got != "╰───────── ↑2 ↓15 ─╯" {
		t.Errorf("readout bottom = %q", got)
	}
	if got := bottom(p.WithScroll(0, 0), 20); got != "╰──────────────────╯" {
		t.Errorf("hidden readout bottom = %q", got)
	}
	if got := bottom(p.WithScroll(-3, 0), 20); strings.Contains(got, "↑") {
		t.Errorf("negative scroll not clamped to hidden: %q", got)
	}
	if got := bottom(p.WithScroll(2, 15), 8); strings.Contains(got, "↑") || lipgloss.Width(got) != 8 {
		t.Errorf("readout not dropped on a narrow edge: %q", got)
	}
}

// TestPaneANSI16SkipsFill: on a 16-colour terminal the fill pass is skipped,
// the content stays intact and the size still holds.
func TestPaneANSI16SkipsFill(t *testing.T) {
	got := NewPane(theme.Dark(), colorprofile.ANSI).WithTitle("T").Render("hello", 12, 4)
	if !strings.Contains(stripANSI(got), "hello") || lipgloss.Width(got) != 12 || lipgloss.Height(got) != 4 {
		t.Errorf("ANSI16 pane wrong:\n%s", stripANSI(got))
	}
}

// TestTabs: exact width, one row, the active label on Selection, and no holes
// in either ground. Load-bearing: the overflow rows prove the active tab
// stays visible when the strip is too narrow for every label.
func TestTabs(t *testing.T) {
	th := theme.Dark()
	labels := []string{"Overview", "Waves", "Tasks"}
	tests := []struct {
		name   string
		tabs   Tabs
		w      int
		prefix string // expected visible start, "" = skip
		has    string // text that must be visible
	}{
		{"fits", NewTabs(th, colorprofile.TrueColor, labels, 0), 40, " Overview  Waves  Tasks ", "Overview"},
		{"exact", NewTabs(th, colorprofile.TrueColor, labels, 1), 24, "", "Waves"},
		{"on panel", NewTabs(th, colorprofile.TrueColor, labels, 1).OnPanel(), 40, "", "Waves"},
		{"overflow keeps active", NewTabs(th, colorprofile.TrueColor, labels, 2), 12, "…", "Tasks"},
		{"cut at right", NewTabs(th, colorprofile.TrueColor, labels, 0), 10, "", "Overvie"},
		{"no active", NewTabs(th, colorprofile.TrueColor, labels, -1), 30, "", "Overview"},
		{"no labels", NewTabs(th, colorprofile.TrueColor, nil, 0), 10, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tabs.Render(tt.w)
			if lipgloss.Width(got) != tt.w || lipgloss.Height(got) != 1 {
				t.Errorf("rendered %dx%d, want %dx1", lipgloss.Width(got), lipgloss.Height(got), tt.w)
			}
			plain := stripANSI(got)
			if !strings.Contains(plain, tt.has) {
				t.Errorf("%q missing from %q", tt.has, plain)
			}
			if tt.prefix != "" && !strings.HasPrefix(plain, tt.prefix) {
				t.Errorf("strip = %q, want prefix %q", plain, tt.prefix)
			}
			assertNoHoles(t, got, tt.w, 1)
		})
	}
	if got := NewTabs(th, colorprofile.TrueColor, labels, 0).Render(0); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}

// TestTabsActiveOnSelection: only the active label carries the Selection
// background.
func TestTabsActiveOnSelection(t *testing.T) {
	th := theme.Dark()
	sel := "48;2;69;71;90"
	one := NewTabs(th, colorprofile.TrueColor, []string{"A", "B"}, 0).Render(10)
	two := NewTabs(th, colorprofile.TrueColor, []string{"A", "B"}, 1).Render(10)
	if strings.Count(one, sel) != 1 || strings.Count(two, sel) != 1 {
		t.Errorf("want exactly one Selection span per strip:\n%q\n%q", one, two)
	}
	if one == two {
		t.Error("changing the active tab did not change the strip")
	}
}
