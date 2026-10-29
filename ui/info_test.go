package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/theme"
)

// numberedBody returns a body of n lines, the first recording the width it
// was asked to render at, so a golden frame proves Body saw the real width.
func numberedBody(n int, tag *string) func(int) []string {
	th := theme.Dark()
	return func(width int) []string {
		lines := []string{fmt.Sprintf("%s width=%d", *tag, width)}
		for i := 1; i < n; i++ {
			// A styled span ends in a reset, leaving the rest of the row
			// background-less: the shape the card's fill pass must close.
			lines = append(lines, th.On(th.Accent, th.Surface).Render(fmt.Sprintf("line %02d", i))+" detail")
		}
		return lines
	}
}

// TestInfoPanelRenders covers the golden frames at two widths: a short body
// that fits, a long body scrolled down, and a body that changes on a refresh
// tick. Load-bearing: zero holes on every frame, and the natural frame must
// equal NaturalSize.
func TestInfoPanelRenders(t *testing.T) {
	th := theme.Dark()
	for _, width := range []int{44, 72} {
		size := tea.WindowSizeMsg{Width: width, Height: 10}

		t.Run(fmt.Sprintf("short/%d", width), func(t *testing.T) {
			tag := "short"
			p := NewInfoPanel(th, colorprofile.TrueColor, "Session info", numberedBody(4, &tag))
			var m tea.Model = p
			m, _ = m.Update(size)
			checkFrame(t, m, width, 10, fmt.Sprintf("info-short-%d", width))
		})

		t.Run(fmt.Sprintf("scrolled/%d", width), func(t *testing.T) {
			tag := "long"
			p := NewInfoPanel(th, colorprofile.TrueColor, "Session info", numberedBody(30, &tag))
			var m tea.Model = p
			m, _ = m.Update(size)
			for range 5 {
				m, _ = press(m, tea.KeyDown)
			}
			checkFrame(t, m, width, 10, fmt.Sprintf("info-scrolled-%d", width))
		})

		t.Run(fmt.Sprintf("tick/%d", width), func(t *testing.T) {
			tag := "before"
			p := NewInfoPanel(th, colorprofile.TrueColor, "Session info", numberedBody(6, &tag)).
				WithRefresh(time.Second)
			var m tea.Model = p
			m, _ = m.Update(size)
			tag = "after"
			m, cmd := m.Update(infoTickMsg{})
			if cmd == nil {
				t.Fatal("tick did not schedule the next refresh")
			}
			checkFrame(t, m, width, 10, fmt.Sprintf("info-tick-%d", width))
		})
	}

	t.Run("natural", func(t *testing.T) {
		tag := "natural"
		p := NewInfoPanel(th, colorprofile.TrueColor, "Session info", numberedBody(5, &tag))
		w, h := p.NaturalSize()
		checkFrame(t, p, w, h, "info-natural")
	})
}

// checkFrame asserts m's frame is w×h, has no holes and matches its golden.
func checkFrame(t *testing.T, m tea.Model, w, h int, golden string) {
	t.Helper()
	frame := m.View().Content
	if gw, gh := lipgloss.Width(frame), lipgloss.Height(frame); gw != w || gh != h {
		t.Errorf("rendered %dx%d, want %dx%d", gw, gh, w, h)
	}
	assertNoHoles(t, frame, w, h)
	assertGolden(t, golden, frame)
}

// TestInfoPanelScrollClamps: scrolling stops at both ends, and a refresh that
// shrinks the body pulls the offset back so the window is never past the end.
func TestInfoPanelScrollClamps(t *testing.T) {
	lines := 30
	p := NewInfoPanel(theme.Dark(), colorprofile.TrueColor, "T", func(int) []string {
		out := make([]string, lines)
		for i := range out {
			out[i] = fmt.Sprintf("row %d", i)
		}
		return out
	}).WithRefresh(time.Second)
	var m tea.Model = p
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})

	m, _ = press(m, tea.KeyUp)
	if p.offset != 0 {
		t.Errorf("scrolled above the top: offset %d", p.offset)
	}
	m, _ = press(m, tea.KeyEnd)
	if want := lines - p.visibleRows(); p.offset != want {
		t.Errorf("end: offset %d, want %d", p.offset, want)
	}
	m, _ = press(m, tea.KeyDown)
	if want := lines - p.visibleRows(); p.offset != want {
		t.Errorf("scrolled past the bottom: offset %d, want %d", p.offset, want)
	}
	lines = 8
	m, _ = m.Update(infoTickMsg{})
	if want := max(lines-p.visibleRows(), 0); p.offset != want {
		t.Errorf("after shrink: offset %d, want %d", p.offset, want)
	}
	assertNoHoles(t, m.View().Content, 40, 10)
}

// TestInfoPanelKeys covers closing and the refresh schedule.
func TestInfoPanelKeys(t *testing.T) {
	tag := "k"
	newPanel := func() *InfoPanel {
		return NewInfoPanel(theme.Dark(), colorprofile.TrueColor, "T", numberedBody(3, &tag))
	}
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'q', Text: "q"}} {
		_, cmd := newPanel().Update(key)
		if cmd == nil || cmd() != (InfoClosedMsg{}) {
			t.Errorf("key %q did not close", key.String())
		}
	}
	bgReq := fmt.Sprintf("%T", tea.RequestBackgroundColor())
	if got := fmt.Sprintf("%T", newPanel().Init()()); got != bgReq {
		t.Errorf("Init without WithRefresh sent %s, want only the background request %s", got, bgReq)
	}
	batch, ok := newPanel().WithRefresh(time.Second).Init()().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Errorf("Init with WithRefresh = %v, want the background request and a tick", batch)
	}
}

// TestInfoPanelRebuild: Rebuild calls Body again without a tick.
func TestInfoPanelRebuild(t *testing.T) {
	n := 0
	p := NewInfoPanel(theme.Dark(), colorprofile.TrueColor, "T", func(int) []string {
		n++
		return []string{fmt.Sprint("call ", n)}
	})
	p.Rebuild()
	if !strings.Contains(ansi.Strip(p.View().Content), "call 2") {
		t.Errorf("after Rebuild the panel shows %q, want the second Body call", ansi.Strip(p.View().Content))
	}
}

// TestInfoPanelOnFirstPaint: fires once after the first frame only.
func TestInfoPanelOnFirstPaint(t *testing.T) {
	tag := "p"
	var n int
	p := NewInfoPanel(theme.Dark(), colorprofile.TrueColor, "T", numberedBody(3, &tag)).
		OnFirstPaint(func() { n++ })
	if n != 0 {
		t.Fatalf("fired before any View: %d", n)
	}
	var m tea.Model = p
	for range 3 {
		m.View()
		m, _ = press(m, tea.KeyDown)
	}
	if n != 1 {
		t.Errorf("OnFirstPaint fired %d times, want 1", n)
	}
}
