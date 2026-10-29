package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/theme"
)

// NoticeKind picks a Notice's colour and glyph.
type NoticeKind int

const (
	// Info is neutral news, drawn in Accent.
	Info NoticeKind = iota
	// Success is a finished action, drawn in Done.
	Success
	// Warning is something that needs the user's attention, drawn in Need.
	Warning
	// Error is a failure, drawn in Danger.
	Error
)

// Notice is a one-line toast or banner. It is a pure render value: it owns no
// timer and returns no tea.Cmd, so dismissal (a timeout, a keypress) stays
// with the caller, who simply stops rendering it.
type Notice struct {
	Kind NoticeKind
	Text string
	// Spinner, when non-empty, replaces the kind glyph. The caller supplies
	// the current frame (for example from a bubbles spinner) and re-renders;
	// the Notice never animates itself.
	Spinner string
}

// glyph is the marker drawn before the text when no Spinner is set. Plain
// single-cell symbols, so the row width stays predictable.
func (k NoticeKind) glyph() string {
	switch k {
	case Success:
		return "✓"
	case Warning:
		return "!"
	case Error:
		return "✗"
	default:
		return "i"
	}
}

// color maps the kind onto its theme token.
func (k NoticeKind) color(t theme.Theme) color.Color {
	switch k {
	case Success:
		return t.Done
	case Warning:
		return t.Need
	case Error:
		return t.Danger
	default:
		return t.Accent
	}
}

// Render returns the notice as one row exactly width cells wide on the
// theme's Panel, which every status colour clears WCAG AA on. Text that does
// not fit is cut with an ellipsis; embedded newlines are flattened to
// spaces so the notice never grows past one row. width below one yields an
// empty string.
func (n Notice) Render(t theme.Theme, width int) string {
	if width < 1 {
		return ""
	}
	mark := n.Spinner
	if mark == "" {
		mark = n.Kind.glyph()
	}
	text := strings.Join(strings.Fields(n.Text), " ")

	markStyle := t.On(n.Kind.color(t), t.Panel).Bold(true)
	textStyle := t.On(t.Text, t.Panel)
	blank := t.On(t.Text, t.Panel)

	inner := max(width-2*hintPad, 0)
	if lipgloss.Width(mark)+1 > inner {
		mark = xansi.Truncate(mark, inner, "")
		text = ""
	}
	var body string
	if text == "" {
		body = markStyle.Render(mark)
	} else {
		text = xansi.Truncate(text, inner-lipgloss.Width(mark)-1, "…")
		body = markStyle.Render(mark) + blank.Render(" ") + textStyle.Render(text)
	}

	lead := min(hintPad, width)
	pad := width - lead - lipgloss.Width(body)
	return blank.Render(strings.Repeat(" ", lead)) + body + blank.Render(strings.Repeat(" ", max(pad, 0)))
}
