package program

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dphaener/rexuikit/surface"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// TestDedupe drives sequences of reports at given offsets. Load-bearing:
// "doubled focus collapses" (Rex with the app drawing), "single report
// passes" (Rex without it: pair-counting would swallow every other real
// change here), and "same report after the window" (a later identical
// report is a new change, not a duplicate).
func TestDedupe(t *testing.T) {
	type step struct {
		at  time.Duration
		msg tea.Msg
	}
	focus, blur := tea.FocusMsg{}, tea.BlurMsg{}
	key := tea.KeyPressMsg{Code: 'a', Text: "a"}
	tests := []struct {
		name  string
		steps []step
		want  []bool // duplicate per step
	}{
		{"doubled focus collapses", []step{{0, focus}, {1 * time.Millisecond, focus}}, []bool{false, true}},
		{"doubled blur collapses", []step{{0, blur}, {2 * time.Millisecond, blur}}, []bool{false, true}},
		{"single report passes", []step{{0, focus}, {100 * time.Millisecond, blur}, {200 * time.Millisecond, focus}}, []bool{false, false, false}},
		{"one in two out", []step{{0, focus}, {500 * time.Millisecond, blur}, {501 * time.Millisecond, blur}}, []bool{false, false, true}},
		{"alternating is never a duplicate", []step{{0, focus}, {1 * time.Millisecond, blur}, {2 * time.Millisecond, focus}, {3 * time.Millisecond, blur}}, []bool{false, false, false, false}},
		{"same report after the window", []step{{0, blur}, {60 * time.Millisecond, blur}}, []bool{false, false}},
		{"other messages pass and do not reset", []step{{0, focus}, {1 * time.Millisecond, key}, {2 * time.Millisecond, focus}}, []bool{false, false, true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &clock{t: time.Unix(1000, 0)}
			d := Dedupe{Now: c.now}
			base := c.t
			for i, s := range tt.steps {
				c.t = base.Add(s.at)
				if got := d.Duplicate(s.msg); got != tt.want[i] {
					t.Errorf("step %d (%T at %v): duplicate = %v, want %v", i, s.msg, s.at, got, tt.want[i])
				}
			}
		})
	}
}

// recorder is an inner model that counts the messages it sees.
type recorder struct {
	seen []tea.Msg
	cmd  tea.Cmd
}

func (r *recorder) Init() tea.Cmd { return nil }

func (r *recorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	r.seen = append(r.seen, msg)
	return r, r.cmd
}

func (r *recorder) View() tea.View { return tea.NewView("x") }

// sequenceCmds unpacks tea.Sequence's unexported message, a []tea.Cmd
// underneath, by kind rather than by name.
func sequenceCmds(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	out := make([]tea.Cmd, v.Len())
	for i := range out {
		out[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return out, true
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if isQuit(c) {
				return true
			}
		}
	default:
		// tea.Sequence returns an unexported sequenceMsg ([]tea.Cmd); its
		// last command is the quit.
		if cmds, ok := sequenceCmds(msg); ok && len(cmds) > 0 {
			return isQuit(cmds[len(cmds)-1])
		}
	}
	return false
}

// TestBlurPolicy: CloseOnBlur quits on a blur (after the inner model saw it
// and its command ran first), StayOpen never does, and neither quits on
// focus.
func TestBlurPolicy(t *testing.T) {
	marker := func() tea.Msg { return "inner" }
	tests := []struct {
		name     string
		policy   surface.BlurPolicy
		msg      tea.Msg
		innerCmd tea.Cmd
		wantQuit bool
	}{
		{"close on blur quits", surface.CloseOnBlur, tea.BlurMsg{}, nil, true},
		{"close on blur keeps inner cmd", surface.CloseOnBlur, tea.BlurMsg{}, marker, true},
		{"close on blur ignores focus", surface.CloseOnBlur, tea.FocusMsg{}, nil, false},
		{"stay open on blur", surface.StayOpen, tea.BlurMsg{}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recorder{cmd: tt.innerCmd}
			m := Wrap(r, Options{Blur: tt.policy})
			_, cmd := m.Update(tt.msg)
			if got := isQuit(cmd); got != tt.wantQuit {
				t.Errorf("quit = %v, want %v", got, tt.wantQuit)
			}
			if len(r.seen) != 1 {
				t.Errorf("inner saw %d messages, want 1", len(r.seen))
			}
			if tt.innerCmd != nil {
				cmds, ok := sequenceCmds(cmd())
				if !ok || len(cmds) != 2 || cmds[0]() != "inner" {
					t.Errorf("inner cmd not run before quit: %#v", cmd())
				}
			}
		})
	}
}

// TestWrapDropsDuplicates: the inner model never sees the second of a
// doubled report, so a CloseOnBlur program quits once and a counting model
// counts one change.
func TestWrapDropsDuplicates(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := &recorder{}
	var m tea.Model = Wrap(r, Options{Blur: surface.StayOpen, Dedupe: Dedupe{Now: c.now}})
	for _, msg := range []tea.Msg{tea.FocusMsg{}, tea.FocusMsg{}, tea.BlurMsg{}, tea.BlurMsg{}} {
		m, _ = m.Update(msg)
		c.t = c.t.Add(time.Millisecond)
	}
	if len(r.seen) != 2 {
		t.Errorf("inner saw %d reports, want 2: %v", len(r.seen), r.seen)
	}
	if Unwrap(m) != r {
		t.Error("Unwrap did not return the inner model")
	}
	if Unwrap(r) != r {
		t.Error("Unwrap of an unwrapped model changed it")
	}
}

// TestViewReportsFocus: bubbletea v2 turns DECSET 1004 on from the view, so
// the wrapper must set it on every frame.
func TestViewReportsFocus(t *testing.T) {
	v := Wrap(&recorder{}, Options{}).View()
	if !v.ReportFocus {
		t.Error("ReportFocus is off")
	}
	if v.Content != "x" {
		t.Errorf("content = %q, want the inner view", v.Content)
	}
}
