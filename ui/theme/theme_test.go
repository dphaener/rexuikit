package theme

import (
	"image/color"
	"math"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestContrastKnownValues pins Contrast against the two values WCAG defines
// outright; without it the contrast assertions below could pass on a wrong
// formula.
func TestContrastKnownValues(t *testing.T) {
	black, white := lipgloss.Color("#000000"), lipgloss.Color("#ffffff")
	if got := Contrast(black, white); math.Abs(got-21) > 0.001 {
		t.Errorf("black on white = %.3f, want 21", got)
	}
	if got := Contrast(white, white); math.Abs(got-1) > 0.001 {
		t.Errorf("white on white = %.3f, want 1", got)
	}
}

// TestTextTokensReadableOnBothGrounds is the contrast acceptance: every text
// token clears WCAG AA on Surface (the base) and Well (fields and the hint
// bar sit on it) in both themes. Colours are read straight off the tokens, no
// rendering.
func TestTextTokensReadableOnBothGrounds(t *testing.T) {
	const aa = 4.5
	for name, th := range map[string]Theme{"dark": Dark(), "light": Light()} {
		for _, ground := range []struct {
			name string
			bg   color.Color
		}{{"surface", th.Surface}, {"well", th.Well}} {
			for _, tok := range []struct {
				name string
				fg   color.Color
			}{{"text", th.Text}, {"muted", th.Muted}, {"accent", th.Accent}, {"danger", th.Danger}} {
				t.Run(name+"/"+tok.name+"/"+ground.name, func(t *testing.T) {
					if got := Contrast(tok.fg, ground.bg); got < aa {
						t.Errorf("contrast = %.2f, want >= %.1f", got, aa)
					}
				})
			}
		}
	}
}

// TestOnAlwaysSetsBothColours: the style helper exists so a component cannot
// forget the background; a style with a NoColor background would be the bug.
func TestOnAlwaysSetsBothColours(t *testing.T) {
	th := Dark()
	s := th.On(th.Accent, th.Well)
	if _, none := s.GetForeground().(lipgloss.NoColor); none {
		t.Error("On left the foreground unset")
	}
	if _, none := s.GetBackground().(lipgloss.NoColor); none {
		t.Error("On left the background unset")
	}
	if Contrast(s.GetForeground(), th.Accent) > 1.001 || Contrast(s.GetBackground(), th.Well) > 1.001 {
		t.Error("On did not use the colours it was given")
	}
	body := th.Body()
	if Contrast(body.GetForeground(), th.Text) > 1.001 || Contrast(body.GetBackground(), th.Surface) > 1.001 {
		t.Error("Body is not Text on Surface")
	}
}

// TestFromBackground covers both extremes, the mid-grey on each side of the
// equal-contrast threshold, and the pre-BackgroundColorMsg nil.
func TestFromBackground(t *testing.T) {
	dark, light := Dark(), Light()
	tests := []struct {
		name string
		bg   color.Color
		want Theme
	}{
		{"black", lipgloss.Color("#000000"), dark},
		{"white", lipgloss.Color("#ffffff"), light},
		{"spike surface", lipgloss.Color("#1e1e2e"), dark},
		{"latte surface", lipgloss.Color("#eff1f5"), light},
		{"grey just dark of threshold", lipgloss.Color("#757575"), dark},
		{"grey just light of threshold", lipgloss.Color("#777777"), light},
		{"nil before any reply", nil, dark},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromBackground(tt.bg)
			if Contrast(got.Surface, tt.want.Surface) > 1.001 {
				t.Errorf("picked the wrong theme (surface %v)", got.Surface)
			}
		})
	}
}
