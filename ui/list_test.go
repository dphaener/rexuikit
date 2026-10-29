package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

func testRows() []Row {
	return []Row{
		{Label: "main", Detail: "default"},
		{Label: "feat/rex-tui-components", Detail: "3 ahead"},
		{Label: "feat/forge-go"},
		{Label: "fix/overlay-holes", Detail: "1 ahead"},
		{Label: "chore/deps"},
	}
}

func typeText(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func press(m tea.Model, code rune) (tea.Model, tea.Cmd) {
	return m.Update(tea.KeyPressMsg{Code: code})
}

// TestListRenders covers the golden frames: unfiltered, filtered and empty,
// each at the natural size and in a roomier 60-column frame. Load-bearing:
// every row asserts zero holes, and the natural frame must be exactly
// NaturalSize, since a surface sizes the Rex layer from it.
func TestListRenders(t *testing.T) {
	th := theme.Dark()
	tests := []struct {
		name  string
		rows  []Row
		query string
		title string
	}{
		{"all", testRows(), "", "Base branch"},
		{"filtered", testRows(), "feat", "Base branch"},
		{"empty", nil, "", ""},
		{"no-match", testRows(), "zzz", ""},
	}
	for _, tt := range tests {
		for _, size := range []struct {
			label string
			w, h  int
		}{{"natural", 0, 0}, {"wide", 60, 12}} {
			t.Run(tt.name+"/"+size.label, func(t *testing.T) {
				l := NewList(th, colorprofile.TrueColor, tt.rows)
				if tt.title != "" {
					l.WithTitle(tt.title)
				}
				var m tea.Model = l
				m = typeText(m, tt.query)
				w, h := l.NaturalSize()
				if size.w > 0 {
					w, h = size.w, size.h
					m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				}
				frame := m.View().Content
				if gw, gh := lipgloss.Width(frame), lipgloss.Height(frame); gw != w || gh != h {
					t.Errorf("rendered %dx%d, want %dx%d", gw, gh, w, h)
				}
				assertNoHoles(t, frame, w, h)
				assertGolden(t, fmt.Sprintf("list-%s-%s", tt.name, size.label), frame)
			})
		}
	}
}

// TestListNaturalSizeIgnoresFilter: the layer is sized once from the full
// list, so typing must not change the size a surface would ask for.
func TestListNaturalSizeIgnoresFilter(t *testing.T) {
	l := NewList(theme.Dark(), colorprofile.TrueColor, testRows())
	w0, h0 := l.NaturalSize()
	typeText(l, "feat")
	if w, h := l.NaturalSize(); w != w0 || h != h0 {
		t.Errorf("natural size moved from %dx%d to %dx%d after filtering", w0, h0, w, h)
	}
}

// TestListKeys covers selection and cancellation. Load-bearing: the selected
// Index is the position in the original rows, not in the filtered view.
func TestListKeys(t *testing.T) {
	newList := func() tea.Model { return NewList(theme.Dark(), colorprofile.TrueColor, testRows()) }
	tests := []struct {
		name string
		run  func() (tea.Model, tea.Cmd)
		want tea.Msg
	}{
		{"enter selects first", func() (tea.Model, tea.Cmd) { return press(newList(), tea.KeyEnter) },
			ListSelectedMsg{Index: 0, Row: testRows()[0]}},
		{"down then enter", func() (tea.Model, tea.Cmd) {
			m, _ := press(newList(), tea.KeyDown)
			return press(m, tea.KeyEnter)
		}, ListSelectedMsg{Index: 1, Row: testRows()[1]}},
		{"filter maps to original index", func() (tea.Model, tea.Cmd) {
			return press(typeText(newList(), "chore"), tea.KeyEnter)
		}, ListSelectedMsg{Index: 4, Row: testRows()[4]}},
		{"filter matches detail", func() (tea.Model, tea.Cmd) {
			return press(typeText(newList(), "1 ahead"), tea.KeyEnter)
		}, ListSelectedMsg{Index: 3, Row: testRows()[3]}},
		{"backspace widens filter", func() (tea.Model, tea.Cmd) {
			m := typeText(newList(), "chorex")
			m, _ = press(m, tea.KeyBackspace)
			return press(m, tea.KeyEnter)
		}, ListSelectedMsg{Index: 4, Row: testRows()[4]}},
		{"end then enter", func() (tea.Model, tea.Cmd) {
			m, _ := press(newList(), tea.KeyEnd)
			return press(m, tea.KeyEnter)
		}, ListSelectedMsg{Index: 4, Row: testRows()[4]}},
		{"esc cancels", func() (tea.Model, tea.Cmd) { return press(newList(), tea.KeyEscape) },
			ListCancelledMsg{}},
		{"enter with no match does nothing", func() (tea.Model, tea.Cmd) {
			return press(typeText(newList(), "zzz"), tea.KeyEnter)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := tt.run()
			var got tea.Msg
			if cmd != nil {
				got = cmd()
			}
			if got != tt.want {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestListScrollKeepsCursorVisible: in a frame too short for every row the
// window follows the cursor, and the cursor row is always drawn.
func TestListScrollKeepsCursorVisible(t *testing.T) {
	rows := make([]Row, 30)
	for i := range rows {
		rows[i] = Row{Label: fmt.Sprintf("row-%02d", i)}
	}
	var m tea.Model = NewList(theme.Dark(), colorprofile.TrueColor, rows)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 9})
	for i := range 20 {
		m, _ = press(m, tea.KeyDown)
		frame := stripANSI(m.View().Content)
		if want := fmt.Sprintf("› row-%02d", i+1); !strings.Contains(frame, want) {
			t.Fatalf("after %d downs the cursor row %q is not visible:\n%s", i+1, want, frame)
		}
	}
}

// TestListOnFirstPaint: the callback fires once, after the first frame, and
// never again however many frames follow.
func TestListOnFirstPaint(t *testing.T) {
	var n int
	l := NewList(theme.Dark(), colorprofile.TrueColor, testRows()).OnFirstPaint(func() { n++ })
	if n != 0 {
		t.Fatalf("fired before any View: %d", n)
	}
	var m tea.Model = l
	for range 3 {
		m.View()
		m = typeText(m, "a")
	}
	if n != 1 {
		t.Errorf("OnFirstPaint fired %d times, want 1", n)
	}
}
