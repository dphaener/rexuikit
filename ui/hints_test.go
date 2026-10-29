package ui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/dphaener/rexuikit/ui/theme"
)

var testHints = []Hint{{"enter", "open"}, {"r", "resume"}, {"x", "close"}, {"q", "quit"}}

// TestHintBarFits covers width fitting and drop order. Load-bearing: the row
// is exactly width cells at every width, trailing hints go first and whole,
// and the status survives until it alone overflows.
func TestHintBarFits(t *testing.T) {
	th := theme.Dark()
	cases := []struct {
		name   string
		status string
		width  int
		want   string // ANSI-stripped, trailing pad included
	}{
		{"all fit", "", 60, " enter open  r resume  x close  q quit" + strings.Repeat(" ", 60-38)},
		{"drops last", "", 31, " enter open  r resume  x close" + " "},
		{"drops two", "", 22, " enter open  r resume" + " "},
		{"drops all but first", "", 12, " enter open "},
		{"first too wide", "", 8, "        "},
		{"status then hints", "3 sessions", 40, " 3 sessions  enter open  r resume" + strings.Repeat(" ", 40-33)},
		{"status keeps over hints", "3 sessions", 14, " 3 sessions   "},
		{"status truncated", "a very long status", 10, " a very … "},
		{"width one", "x", 1, " "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := NewHintBar(th).WithStatus(c.status).Render(testHints, c.width)
			if w := lipgloss.Width(row); w != c.width {
				t.Fatalf("width = %d, want %d", w, c.width)
			}
			if got := stripANSI(row); got != c.want {
				t.Errorf("row = %q, want %q", got, c.want)
			}
			assertNoHoles(t, row, c.width, 1)
		})
	}
	if got := NewHintBar(th).Render(testHints, 0); got != "" {
		t.Errorf("width 0 = %q, want empty", got)
	}
}

// TestHintBarPaint: the row has no see-through cell in either theme and sits
// on Well, not on the default background.
func TestHintBarPaint(t *testing.T) {
	for name, th := range map[string]theme.Theme{"dark": theme.Dark(), "light": theme.Light()} {
		t.Run(name, func(t *testing.T) {
			for _, w := range []int{10, 24, 50, 80} {
				row := NewHintBar(th).WithStatus("status").Render(testHints, w)
				assertNoHoles(t, row, w, 1)
			}
		})
	}
}

// TestNoticeKinds: each kind maps to its token on Panel with AA contrast, the
// glyph differs per kind, and a spinner replaces the glyph.
func TestNoticeKinds(t *testing.T) {
	for name, th := range map[string]theme.Theme{"dark": theme.Dark(), "light": theme.Light()} {
		kinds := []struct {
			kind  NoticeKind
			glyph string
			fg    func(theme.Theme) any
		}{
			{Info, "i", func(t theme.Theme) any { return t.Accent }},
			{Success, "✓", func(t theme.Theme) any { return t.Done }},
			{Warning, "!", func(t theme.Theme) any { return t.Need }},
			{Error, "✗", func(t theme.Theme) any { return t.Danger }},
		}
		for _, k := range kinds {
			t.Run(fmt.Sprintf("%s/%d", name, k.kind), func(t *testing.T) {
				if k.kind.color(th) != k.fg(th) {
					t.Errorf("kind %d colour is not its mapped token", k.kind)
				}
				if c := theme.Contrast(k.kind.color(th), th.Panel); c < 4.5 {
					t.Errorf("contrast on Panel = %.2f, want >= 4.5", c)
				}
				row := Notice{Kind: k.kind, Text: "saved"}.Render(th, 20)
				if got, want := stripANSI(row), " "+k.glyph+" saved"+strings.Repeat(" ", 20-utf8.RuneCountInString(" "+k.glyph+" saved")); got != want {
					t.Errorf("row = %q, want %q", got, want)
				}
				assertNoHoles(t, row, 20, 1)
			})
		}
	}

	th := theme.Dark()
	row := stripANSI(Notice{Kind: Info, Text: "working", Spinner: "⠋"}.Render(th, 16))
	if want := " ⠋ working      "; row != want {
		t.Errorf("spinner row = %q, want %q", row, want)
	}
}

// TestNoticeFits: one row at exactly width cells whatever the text, with
// newlines flattened and overflow cut by an ellipsis.
func TestNoticeFits(t *testing.T) {
	th := theme.Dark()
	cases := []struct {
		name string
		n    Notice
		w    int
		want string
	}{
		{"truncates", Notice{Kind: Error, Text: "something went badly wrong"}, 14, " ✗ something… "},
		{"newlines flatten", Notice{Kind: Info, Text: "a\nb\n\nc"}, 12, " i a b c    "},
		{"narrow drops text", Notice{Kind: Warning, Text: "careful"}, 3, " ! "},
		{"width one", Notice{Kind: Success, Text: "ok"}, 1, " "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := c.n.Render(th, c.w)
			if w := lipgloss.Width(row); w != c.w || lipgloss.Height(row) != 1 {
				t.Fatalf("rendered %dx%d, want %dx1", w, lipgloss.Height(row), c.w)
			}
			if got := stripANSI(row); got != c.want {
				t.Errorf("row = %q, want %q", got, c.want)
			}
			assertNoHoles(t, row, c.w, 1)
		})
	}
	if got := (Notice{Text: "x"}).Render(th, 0); got != "" {
		t.Errorf("width 0 = %q, want empty", got)
	}
}
