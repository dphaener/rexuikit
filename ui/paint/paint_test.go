package paint

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"
)

var bg = lipgloss.Color("#1e1e2e")

// spans builds a frame the way components do: styled spans, each ending in an
// SGR reset that clears the background, with unstyled gaps between them. A
// reset-terminated span is exactly the case that leaves holes.
func spans() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")).Bold(true)
	well := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#2a2b3d"))
	return strings.Join([]string{
		accent.Render("Name") + "  " + well.Render("  forge  ") + " end",
		"short",
		well.Render("wide: 日本") + accent.Render("!"),
	}, "\n")
}

// TestFillLeavesNoHoles is the load-bearing row: a frame of reset-terminated
// spans has holes before Fill (so the assertion is not vacuous) and none
// after, over a canvas at least as large as the frame.
func TestFillLeavesNoHoles(t *testing.T) {
	frame := spans()
	w, h := lipgloss.Width(frame), lipgloss.Height(frame)
	if Holes(frame, w, h) == 0 {
		t.Fatal("fixture has no holes before Fill; the test proves nothing")
	}
	got := Fill(frame, bg, colorprofile.TrueColor)
	if n := Holes(got, w, h); n != 0 {
		t.Errorf("Holes after Fill = %d, want 0", n)
	}
	if n := Holes(Fill(frame, bg, colorprofile.ANSI256), w, h); n != 0 {
		t.Errorf("Holes after Fill on ANSI256 = %d, want 0", n)
	}
}

// TestFillKeepsOwnBackgrounds: the well's background must survive, so the
// filled frame still contains the well colour's SGR as well as the surface.
func TestFillKeepsOwnBackgrounds(t *testing.T) {
	got := Fill(spans(), bg, colorprofile.TrueColor)
	if !strings.Contains(got, "48;2;42;43;61") {
		t.Error("the well background was overwritten")
	}
	if !strings.Contains(got, "48;2;30;30;46") {
		t.Error("the surface background was not applied")
	}
}

// TestFillKeepsText: filling only changes styling, never the characters.
func TestFillKeepsText(t *testing.T) {
	frame := spans()
	got := xansi.Strip(Fill(frame, bg, colorprofile.TrueColor))
	for _, want := range []string{"Name", "forge", "short", "日本"} {
		if !strings.Contains(got, want) {
			t.Errorf("filled frame lost %q:\n%s", want, got)
		}
	}
}

// TestFillSkippedOnANSI16: matches forge's behaviour; bg would round to black.
func TestFillSkippedOnANSI16(t *testing.T) {
	frame := spans()
	for _, p := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ASCII, colorprofile.NoTTY, colorprofile.Unknown} {
		if got := Fill(frame, bg, p); got != frame {
			t.Errorf("profile %v: Fill changed the frame", p)
		}
	}
}

// TestHolesCountsKnownHoles: exact counts, computed by hand. A plain 3-cell
// string has 3 holes; the rest of a wider canvas counts too (a frame must
// cover its layer); a styled-bg cell is not a hole; a wide rune's
// continuation cell is not counted separately.
func TestHolesCountsKnownHoles(t *testing.T) {
	onBg := lipgloss.NewStyle().Background(bg)
	tests := []struct {
		name  string
		frame string
		w, h  int
		want  int
	}{
		{"plain text", "abc", 3, 1, 3},
		{"canvas wider than frame", "abc", 5, 1, 5},
		{"canvas taller than frame", "abc", 3, 2, 6},
		{"all painted", onBg.Render("abc"), 3, 1, 0},
		{"one painted of three", onBg.Render("a") + "bc", 3, 1, 2},
		{"trailing painted spaces are not holes", onBg.Render("a  "), 3, 1, 0},
		{"wide rune is one hole", "日", 2, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Holes(tt.frame, tt.w, tt.h); got != tt.want {
				t.Errorf("Holes = %d, want %d", got, tt.want)
			}
		})
	}
}
