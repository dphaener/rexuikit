// Package program is the in-program half of rexuikit/surface: what a
// bubbletea program running inside a surface needs to see focus the way Rex
// delivers it. It is the only part of surface that imports bubbletea, so a
// placer that never runs a TUI does not link it.
//
// Rex forwards terminal focus reports (DECSET 1004) to the focused block,
// but how many depends on the app: the live tests saw exactly one ESC[I and
// one ESC[O per change with no app drawing the window, and one in plus two
// out with the app drawing it (rex-behaviors.md; livetest
// TestFocusReportsForwarded). Dedupe therefore collapses identical
// consecutive reports that arrive close together and never pair-counts, so
// a Rex that sends each report once, twice, or unevenly all read the same.
package program

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dphaener/rexuikit/surface"
)

// DefaultDedupeWindow is how close two identical focus reports must be to
// count as one. Rex's duplicates arrive back to back; a real focus change
// back to the same state needs a different report in between anyway.
const DefaultDedupeWindow = 50 * time.Millisecond

// Dedupe drops a focus report identical to the previous one when it arrives
// within Window of it. The zero value uses DefaultDedupeWindow and the
// wall clock.
type Dedupe struct {
	Window time.Duration
	Now    func() time.Time

	last   tea.Msg
	lastAt time.Time
}

// Duplicate reports whether msg is a FocusMsg or BlurMsg repeating the
// previous report within the window, and records it otherwise. Any other
// message passes through and leaves the state alone: a keypress between two
// identical reports does not make the second one a new change.
func (d *Dedupe) Duplicate(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.FocusMsg, tea.BlurMsg:
	default:
		return false
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	window := d.Window
	if window <= 0 {
		window = DefaultDedupeWindow
	}
	t := now()
	dup := d.last != nil && sameKind(d.last, msg) && t.Sub(d.lastAt) < window
	d.last, d.lastAt = msg, t
	return dup
}

func sameKind(a, b tea.Msg) bool {
	_, af := a.(tea.FocusMsg)
	_, bf := b.(tea.FocusMsg)
	return af == bf
}

// Options configure Wrap.
type Options struct {
	Blur surface.BlurPolicy
	// Dedupe overrides the duplicate filter's window and clock (tests).
	Dedupe Dedupe
}

// Model wraps a program's model: it turns on focus reporting, drops
// duplicate focus reports before the inner model sees them, and applies the
// surface's BlurPolicy.
type Model struct {
	inner  tea.Model
	blur   surface.BlurPolicy
	dedupe *Dedupe
}

// Wrap returns m wrapped. Run the result with tea.NewProgram and Unwrap the
// final model.
func Wrap(m tea.Model, opts Options) Model {
	d := opts.Dedupe
	return Model{inner: m, blur: opts.Blur, dedupe: &d}
}

// Unwrap returns the inner model of a Model, or m itself.
func Unwrap(m tea.Model) tea.Model {
	if w, ok := m.(Model); ok {
		return w.inner
	}
	return m
}

func (m Model) Init() tea.Cmd { return m.inner.Init() }

// Update forwards msg unless it is a duplicate focus report. Under
// CloseOnBlur a blur still reaches the inner model first, so it can tidy up,
// and the program quits after its command.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.dedupe.Duplicate(msg) {
		return m, nil
	}
	inner, cmd := m.inner.Update(msg)
	m.inner = inner
	if _, blur := msg.(tea.BlurMsg); blur && m.blur == surface.CloseOnBlur {
		if cmd == nil {
			return m, tea.Quit
		}
		return m, tea.Sequence(cmd, tea.Quit)
	}
	return m, cmd
}

// View is the inner view with focus reporting on: bubbletea v2 enables
// DECSET 1004 from the view, not from a program option.
func (m Model) View() tea.View {
	v := m.inner.View()
	v.ReportFocus = true
	return v
}
