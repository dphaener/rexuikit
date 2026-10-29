// Package theme holds rexuikit's semantic colour tokens.
//
// A Theme is a plain value: there is no package-level theme variable and no
// setter, so two surfaces in one process (or two tests in parallel) can use
// different themes without racing. Components receive a Theme and derive
// every style from it through On, which sets foreground and background
// together. A style that sets only a foreground leaves the cell on the
// terminal's default background, and inside a Rex floating layer that cell
// shows the terminal underneath; On makes that mistake unwritable.
//
// # First frame and re-theming
//
// Dark is the first-frame default. The terminal's real background reaches a
// bubbletea program as a tea.BackgroundColorMsg, and bubbletea v2 sends it
// only when asked: a program that wants FromBackground must return
// tea.RequestBackgroundColor from Init. The reply is an OSC 11 answer read
// off the terminal, so it arrives after the first frame has been drawn. The
// first frame is therefore always drawn with Dark, and a later re-theme
// (FromBackground on the message's colour) is accepted as a visible one-off
// repaint rather than avoided by blocking the first paint. Whether a Rex
// layer program receives that OSC 11 reply at all is a Rex behaviour recorded
// by W1.T2 in docs/rex-behaviors.md; when it does not, the program simply
// stays on Dark.
package theme

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
)

// Theme is a set of semantic colour tokens. Zero value is not usable: build
// one with Dark, Light or FromBackground.
type Theme struct {
	// Surface is the base background every component sits on.
	Surface color.Color
	// Well is the raised background of an input field or hint bar.
	Well color.Color
	// Text is the primary foreground.
	Text color.Color
	// Muted is the secondary foreground: hints, placeholders, subtitles.
	Muted color.Color
	// Accent marks focus and the primary action.
	Accent color.Color
	// Danger marks errors and destructive actions.
	Danger color.Color
	// Border is the rule colour for the opt-in bordered card and dividers.
	Border color.Color
}

// Dark is the default theme and the first-frame default (see the package
// comment). Its values are the native form spike's palette, except Muted: the
// spike's #7f849c is 4.44:1 on Surface and 3.76:1 on Well, under WCAG AA, so
// it is lifted to #9399b2.
func Dark() Theme {
	return Theme{
		Surface: lipgloss.Color("#1e1e2e"),
		Well:    lipgloss.Color("#2a2b3d"),
		Text:    lipgloss.Color("#cdd6f4"),
		Muted:   lipgloss.Color("#9399b2"),
		Accent:  lipgloss.Color("#89b4fa"),
		Danger:  lipgloss.Color("#f38ba8"),
		Border:  lipgloss.Color("#45475a"),
	}
}

// Light is the light counterpart of Dark: the same hues, darkened until every
// text token clears WCAG AA (4.5:1) on both Surface and Well.
func Light() Theme {
	return Theme{
		Surface: lipgloss.Color("#eff1f5"),
		Well:    lipgloss.Color("#e1e4ec"),
		Text:    lipgloss.Color("#4c4f69"),
		Muted:   lipgloss.Color("#5c5f77"),
		Accent:  lipgloss.Color("#1650d0"),
		Danger:  lipgloss.Color("#c40d33"),
		Border:  lipgloss.Color("#acb0be"),
	}
}

// FromBackground picks Light for a light terminal background and Dark
// otherwise, by relative luminance. The threshold is the luminance at which
// black and white text have equal contrast (about 0.179), so a mid-grey
// background goes to whichever theme reads better on it. A nil colour, the
// state before any BackgroundColorMsg, yields Dark.
func FromBackground(bg color.Color) Theme {
	if bg == nil {
		return Dark()
	}
	if luminance(bg) > 0.179 {
		return Light()
	}
	return Dark()
}

// On returns a style with foreground fg on background bg. Every component
// style goes through this so no cell is left on the default background.
func (t Theme) On(fg, bg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fg).Background(bg)
}

// Body is the style for ordinary text: Text on Surface.
func (t Theme) Body() lipgloss.Style { return t.On(t.Text, t.Surface) }

// Contrast is WCAG 2.1's contrast ratio between two colours, 1 (identical) to
// 21 (black on white).
func Contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance is WCAG 2.1 relative luminance. color.Color.RGBA is 16-bit and
// alpha-premultiplied; the palette is opaque, so the high byte is the channel.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		f := float64(v>>8) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
