package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

// styledContent is form-like content built from spans that each end in an SGR
// reset, the shape that leaves background holes in a naive card.
func styledContent(th theme.Theme) string {
	label := th.On(th.Accent, th.Surface).Bold(true)
	well := th.On(th.Text, th.Well)
	return strings.Join([]string{
		label.Render("Name"),
		well.Render(" my-session      "),
		"",
		label.Render("Base") + " main",
	}, "\n")
}

// TestCardRenders covers the geometry rows: borderless fill edge to edge at
// the natural size and in a roomier frame, with and without subtitle and
// hint. Load-bearing: every row also asserts zero holes, the card's whole
// reason to exist, and the natural size must equal what Render produces.
func TestCardRenders(t *testing.T) {
	th := theme.Dark()
	content := styledContent(th)
	tests := []struct {
		name string
		card Card
		w, h int // 0 = natural
	}{
		{"plain natural", NewCard(th, colorprofile.TrueColor), 0, 0},
		{"subtitle and hint natural", NewCard(th, colorprofile.TrueColor).
			WithSubtitle("Create a session").WithHint("enter submit · esc cancel"), 0, 0},
		{"roomy frame", NewCard(th, colorprofile.TrueColor).
			WithSubtitle("Create a session").WithHint("enter submit · esc cancel"), 50, 14},
		{"bordered", NewCard(th, colorprofile.TrueColor).
			WithHint("esc cancel").Bordered("New session"), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nw, nh := tt.card.NaturalSize(content)
			w, h := tt.w, tt.h
			if w == 0 {
				w = nw
			}
			if h == 0 {
				h = nh
			}
			got := tt.card.Render(content, tt.w, tt.h)
			if gw, gh := lipgloss.Width(got), lipgloss.Height(got); gw != w || gh != h {
				t.Errorf("rendered %dx%d, want %dx%d", gw, gh, w, h)
			}
			assertNoHoles(t, got, w, h)
			assertGolden(t, "card-"+strings.ReplaceAll(tt.name, " ", "-"), got)
		})
	}
}

// TestCardNaturalSize pins the arithmetic: padding 1 row by 2 columns, subtitle
// plus a blank row, a blank row plus the hint bar, border 2 each way.
func TestCardNaturalSize(t *testing.T) {
	th := theme.Dark()
	c := NewCard(th, colorprofile.TrueColor)
	tests := []struct {
		name       string
		card       Card
		content    string
		cols, rows int
	}{
		{"one line", c, "hello", 9, 3},
		{"two lines, widest counts", c, "hi\nhello", 9, 4},
		{"subtitle widens", c.WithSubtitle("a longer subtitle"), "hi", 21, 5},
		{"hint widens and adds the bar", c.WithHint("enter submit"), "hi", 16, 4},
		{"bordered adds two each way", c.Bordered("T"), "hello", 11, 5},
		{"styled content measured by cells", c, th.On(th.Accent, th.Surface).Render("hello"), 9, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows := tt.card.NaturalSize(tt.content)
			if cols != tt.cols || rows != tt.rows {
				t.Errorf("NaturalSize = %dx%d, want %dx%d", cols, rows, tt.cols, tt.rows)
			}
		})
	}
}

// TestCardHintKeepsLastRow: a frame too short for the content cuts content
// rows, never the hint bar, which carries the cancel key.
func TestCardHintKeepsLastRow(t *testing.T) {
	th := theme.Dark()
	c := NewCard(th, colorprofile.TrueColor).WithHint("esc cancel")
	got := stripANSI(c.Render("a\nb\nc\nd\ne", 20, 4))
	lines := strings.Split(got, "\n")
	if len(lines) != 4 || !strings.Contains(lines[3], "esc cancel") {
		t.Errorf("hint not on the last row of a 4-row frame:\n%s", got)
	}
}

// TestCardBorderlessHasNoBorderGlyphs: the default card must not draw a frame;
// Rex's chrome is the frame.
func TestCardBorderlessHasNoBorderGlyphs(t *testing.T) {
	got := stripANSI(NewCard(theme.Dark(), colorprofile.TrueColor).Render("hello", 0, 0))
	if strings.ContainsAny(got, "╭╮╰╯─│") {
		t.Errorf("borderless card drew a border:\n%s", got)
	}
}

// TestCardANSI16SkipsFill: on a 16-colour terminal the card does not paint the
// surface slab (matching forge), so holes are expected and the text intact.
func TestCardANSI16SkipsFill(t *testing.T) {
	got := NewCard(theme.Dark(), colorprofile.ANSI).Render("hello", 0, 0)
	if !strings.Contains(stripANSI(got), "hello") {
		t.Errorf("content lost:\n%s", got)
	}
}
