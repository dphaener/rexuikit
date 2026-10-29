package ui

import (
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

func validName(s string) error {
	if s == "" {
		return errors.New("required")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(s) {
		return errors.New("lowercase letters, digits and dashes only")
	}
	return nil
}

// sessionForm is the fixture most tests drive: a validated name, a static
// select and a confirm.
func sessionForm() (*Form, *Text, *Select, *Confirm) {
	name := NewText("name", "Session name").Placeholder("fix-login-redirect").Validator(validName)
	base := NewSelect("base", "Base branch").Options(
		Option{Label: "main", Value: "main"},
		Option{Label: "rex-tui-components", Value: "rex-tui-components"},
		Option{Label: "feat/other", Value: "feat/other"},
	).Default("main")
	start := NewConfirm("start", "Start now")
	return NewForm(theme.Dark(), colorprofile.TrueColor, name, base, start), name, base, start
}

// formKey builds a key press from its bubbletea name: a named key, a
// modified key ("shift+tab", "ctrl+j", "alt+enter") or a single character.
func formKey(name string) tea.KeyPressMsg {
	named := map[string]rune{"enter": tea.KeyEnter, "tab": tea.KeyTab, "esc": tea.KeyEscape,
		"up": tea.KeyUp, "down": tea.KeyDown, "backspace": tea.KeyBackspace}
	var mod tea.KeyMod
	for _, p := range []struct {
		prefix string
		mod    tea.KeyMod
	}{{"shift+", tea.ModShift}, {"ctrl+", tea.ModCtrl}, {"alt+", tea.ModAlt}} {
		if rest, ok := strings.CutPrefix(name, p.prefix); ok && len(rest) > 0 {
			mod, name = p.mod, rest
		}
	}
	if code, ok := named[name]; ok {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}
	r := []rune(name)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: name}
}

// drain runs cmd and every command it batches, returning the messages they
// produce. Init's background-colour request comes back as bubbletea's
// unexported request message, which a model ignores.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// sendKeys presses each key on f and returns the messages the last
// non-nil command produced.
func sendKeys(f *Form, keys ...string) []tea.Msg {
	var last []tea.Msg
	for _, k := range keys {
		_, cmd := f.Update(formKey(k))
		if msgs := drain(cmd); len(msgs) > 0 {
			last = msgs
		}
	}
	return last
}

// assertNaturalFrame checks a frame drawn without a WindowSizeMsg is exactly
// NaturalSize, which a surface sizes the Rex layer from, and has no holes.
func assertNaturalFrame(t *testing.T, frame string, cols, rows int) {
	t.Helper()
	if w, h := lipgloss.Width(frame), lipgloss.Height(frame); w != cols || h != rows {
		t.Errorf("frame is %dx%d, NaturalSize says %dx%d", w, h, cols, rows)
	}
	assertNoHoles(t, frame, cols, rows)
}

// TestFormRenders covers the form's golden frames. Load-bearing: every row
// asserts zero holes on the raw frame and that the natural frame is exactly
// NaturalSize; the validation and loader error rows prove an error takes
// the status row without changing the size.
func TestFormRenders(t *testing.T) {
	failingLoader := func() ([]Option, string, error) { return nil, "", errors.New("git branch failed: exit 128") }
	tests := []struct {
		name  string
		setup func() *Form
	}{
		{"form-focused-text", func() *Form {
			f, _, _, _ := sessionForm()
			return f
		}},
		{"form-focused-select", func() *Form {
			f, _, _, _ := sessionForm()
			sendKeys(f, "f", "i", "x", "enter", "down")
			return f
		}},
		{"form-validation-error", func() *Form {
			f, _, _, _ := sessionForm()
			sendKeys(f, "F", "enter")
			return f
		}},
		{"form-loader-error", func() *Form {
			name := NewText("name", "Session name").Default("fix-x")
			base := NewSelect("base", "Base branch").Loader(failingLoader)
			f := NewForm(theme.Dark(), colorprofile.TrueColor, name, base)
			for _, msg := range drain(f.Init()) {
				f.Update(msg)
			}
			sendKeys(f, "enter")
			return f
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.setup()
			cols, rows := f.NaturalSize()
			frame := f.Render()
			assertNaturalFrame(t, frame, cols, rows)
			assertGolden(t, tc.name, frame)
		})
	}

	t.Run("form-focused-text-wide", func(t *testing.T) {
		f, _, _, _ := sessionForm()
		_, rows := f.NaturalSize()
		f.Update(tea.WindowSizeMsg{Width: 72, Height: rows + 2})
		frame := f.Render()
		if w, h := lipgloss.Width(frame), lipgloss.Height(frame); w != 72 || h != rows+2 {
			t.Errorf("frame is %dx%d, want the window's 72x%d", w, h, rows+2)
		}
		assertNoHoles(t, frame, 72, rows+2)
		assertGolden(t, "form-focused-text-wide", frame)
	})
}

// TestFormErrorsKeepSize pins the reason the status row exists: an error
// appearing must not change the natural size the layer was opened at.
func TestFormErrorsKeepSize(t *testing.T) {
	f, name, _, _ := sessionForm()
	cols, rows := f.NaturalSize()
	sendKeys(f, "enter")
	if name.Err() == nil {
		t.Fatal("enter on an empty name should show an error")
	}
	if c, r := f.NaturalSize(); c != cols || r != rows {
		t.Errorf("NaturalSize changed from %dx%d to %dx%d when the error appeared", cols, rows, c, r)
	}
}

// TestDialogRenders covers the dialog at its natural width and a wider frame.
// Load-bearing: the body carries a caller-styled span with no background
// (as forge's pre-rendered lines do), which must still come out solid.
func TestDialogRenders(t *testing.T) {
	body := []string{
		"Remove the worktree and branch of",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Render("fix-login-redirect") + "?",
		"",
		"The Rex tab closes with it.",
	}
	for _, tc := range []struct {
		name string
		w    int
	}{{"dialog-natural", 0}, {"dialog-wide", 64}} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDialog(theme.Dark(), colorprofile.TrueColor, "Clean session", body).WithHint("d confirm · esc cancel")
			cols, rows := d.NaturalSize()
			if tc.w > 0 {
				d.Update(tea.WindowSizeMsg{Width: tc.w, Height: rows})
				cols = tc.w
			}
			frame := d.View()
			assertNaturalFrame(t, frame, cols, rows)
			assertGolden(t, tc.name, frame)
		})
	}
}

// TestDialogIgnoresKeys pins that the caller owns key routing: a Dialog
// neither closes on esc nor returns a command for any key.
func TestDialogIgnoresKeys(t *testing.T) {
	d := NewDialog(theme.Dark(), colorprofile.TrueColor, "Clean", []string{"sure?"})
	before := d.View()
	for _, k := range []string{"esc", "enter", "d", "y"} {
		if cmd := d.Update(formKey(k)); cmd != nil {
			t.Errorf("key %q returned a command", k)
		}
	}
	if d.View() != before {
		t.Error("a key changed the dialog's frame")
	}
}

// TestFormNavigation is the navigation table. Load-bearing rows: "enter on
// invalid stays" (the validator gates leaving a field), "submit returns
// values" (typed values in the message and from Values) and "esc cancels".
func TestFormNavigation(t *testing.T) {
	tests := []struct {
		name       string
		keys       []string
		wantFocus  int
		wantStatus FormStatus
		wantMsg    tea.Msg
		wantErr    bool // the name field shows an error
	}{
		{name: "enter on invalid stays", keys: []string{"enter"}, wantFocus: 0, wantErr: true},
		{name: "invalid value stays", keys: []string{"A", "tab"}, wantFocus: 0, wantErr: true},
		{name: "editing clears the error", keys: []string{"enter", "f"}, wantFocus: 0},
		{name: "valid enter advances", keys: []string{"f", "x", "enter"}, wantFocus: 1},
		{name: "tab advances like enter", keys: []string{"f", "x", "tab"}, wantFocus: 1},
		{name: "shift+tab goes back", keys: []string{"f", "x", "enter", "shift+tab"}, wantFocus: 0},
		{name: "shift+tab at first stays", keys: []string{"shift+tab"}, wantFocus: 0},
		{
			name:       "submit returns values",
			keys:       []string{"f", "x", "enter", "down", "enter", "y", "enter"},
			wantFocus:  2,
			wantStatus: FormSubmitted,
			wantMsg: FormSubmittedMsg{Values: Values{
				"name": "fx", "base": "rex-tui-components", "start": true,
			}},
		},
		{name: "esc cancels", keys: []string{"f", "esc"}, wantStatus: FormCancelled, wantMsg: FormCancelledMsg{}},
		{name: "keys after cancel are ignored", keys: []string{"esc", "f", "enter"}, wantStatus: FormCancelled, wantMsg: FormCancelledMsg{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, name, _, _ := sessionForm()
			msgs := sendKeys(f, tc.keys...)
			if f.Focused() != tc.wantFocus {
				t.Errorf("focus = %d, want %d", f.Focused(), tc.wantFocus)
			}
			if f.Status() != tc.wantStatus {
				t.Errorf("status = %d, want %d", f.Status(), tc.wantStatus)
			}
			if (name.Err() != nil) != tc.wantErr {
				t.Errorf("name error = %v, want error %v", name.Err(), tc.wantErr)
			}
			var got tea.Msg
			if len(msgs) > 0 {
				got = msgs[len(msgs)-1]
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.wantMsg) {
				t.Errorf("last message = %#v, want %#v", got, tc.wantMsg)
			}
		})
	}

	t.Run("typed accessors", func(t *testing.T) {
		f, name, base, start := sessionForm()
		sendKeys(f, "f", "x", "enter", "down", "enter", "y", "enter")
		v := f.Values()
		if v.String("name") != "fx" || name.Get() != "fx" {
			t.Errorf("name = %q / %q, want fx", v.String("name"), name.Get())
		}
		if v.String("base") != "rex-tui-components" || base.Get() != "rex-tui-components" {
			t.Errorf("base = %q / %q", v.String("base"), base.Get())
		}
		if !v.Bool("start") || !start.Get() {
			t.Error("start should be true")
		}
	})
}

// TestFormAsyncSelect covers the loader path end to end through Init.
// Load-bearing: the loading row and the loaded options take the same number
// of rows (the layout never jumps), the default is preselected, and enter
// while loading does not submit.
func TestFormAsyncSelect(t *testing.T) {
	calls := 0
	base := NewSelect("base", "Base branch").Loader(func() ([]Option, string, error) {
		calls++
		return []Option{{"main", "main"}, {"develop", "develop"}, {"feat/x", "feat/x"}}, "develop", nil
	})
	f := NewForm(theme.Dark(), colorprofile.TrueColor, base)
	if calls != 0 {
		t.Fatal("the loader ran before Init's command did")
	}
	cols, rows := f.NaturalSize()
	if !strings.Contains(stripANSI(f.Render()), "loading…") {
		t.Errorf("a pending select should show the loading row:\n%s", stripANSI(f.Render()))
	}
	if msgs := sendKeys(f, "enter"); len(msgs) != 0 || f.Status() != FormPending {
		t.Fatalf("enter while loading submitted: %v", msgs)
	}
	if !errors.Is(base.Err(), errLoading) {
		t.Errorf("err while loading = %v, want %v", base.Err(), errLoading)
	}

	var sawOptions bool
	for _, msg := range drain(f.Init()) {
		if _, ok := msg.(OptionsMsg); ok {
			sawOptions = true
		}
		f.Update(msg)
	}
	if !sawOptions || calls != 1 {
		t.Fatalf("Init should run the loader once and produce an OptionsMsg (calls=%d)", calls)
	}
	if base.Get() != "develop" {
		t.Errorf("preselected %q, want the loader's default develop", base.Get())
	}
	frame := f.Render()
	if !strings.Contains(stripANSI(frame), "› develop") {
		t.Errorf("the default should carry the marker:\n%s", stripANSI(frame))
	}
	if c, r := f.NaturalSize(); c != cols || r != rows {
		t.Errorf("NaturalSize jumped from %dx%d to %dx%d after load", cols, rows, c, r)
	}
	assertNaturalFrame(t, frame, cols, rows)

	msgs := sendKeys(f, "enter")
	if f.Status() != FormSubmitted || len(msgs) != 1 {
		t.Fatalf("enter after load should submit, got status %d and %v", f.Status(), msgs)
	}
	if got := msgs[0].(FormSubmittedMsg).Values.String("base"); got != "develop" {
		t.Errorf("submitted base = %q", got)
	}
}

// TestFormLoaderErrorBlocksSubmit: a failed loader shows its error and
// enter cannot get past it; only esc leaves.
func TestFormLoaderErrorBlocksSubmit(t *testing.T) {
	base := NewSelect("base", "Base branch").Loader(func() ([]Option, string, error) {
		return nil, "", errors.New("not a git repository")
	})
	f := NewForm(theme.Dark(), colorprofile.TrueColor, base)
	for _, msg := range drain(f.Init()) {
		f.Update(msg)
	}
	if !strings.Contains(stripANSI(f.Render()), "✗ not a git repository") {
		t.Errorf("the loader error should show in the field:\n%s", stripANSI(f.Render()))
	}
	for range 3 {
		if msgs := sendKeys(f, "enter"); len(msgs) != 0 {
			t.Fatalf("enter with a loader error produced %v", msgs)
		}
	}
	if f.Status() != FormPending {
		t.Fatalf("status = %d, want pending", f.Status())
	}
	if msgs := sendKeys(f, "esc"); len(msgs) != 1 || f.Status() != FormCancelled {
		t.Errorf("esc should cancel, got %v", msgs)
	}
}

// TestPendingSelectTakesCallerOptions covers the caller-driven path: a
// Pending select is filled by an OptionsMsg the caller sends, and ignores
// one addressed to another select.
func TestPendingSelectTakesCallerOptions(t *testing.T) {
	a := NewSelect("a", "A").Pending()
	b := NewSelect("b", "B").Pending()
	f := NewForm(theme.Dark(), colorprofile.TrueColor, a, b)
	f.Update(OptionsMsg{Select: b, Options: []Option{{"one", "1"}}})
	if a.Get() != "" || b.Get() != "1" {
		t.Errorf("a=%q b=%q, want the message to reach b only", a.Get(), b.Get())
	}
}

// TestOnFirstPaintFiresOnce covers every host in this file.
func TestOnFirstPaintFiresOnce(t *testing.T) {
	var formPaints, dialogPaints int
	f, _, _, _ := sessionForm()
	f.OnFirstPaint(func() { formPaints++ })
	d := NewDialog(theme.Dark(), colorprofile.TrueColor, "t", []string{"b"}).OnFirstPaint(func() { dialogPaints++ })
	if formPaints != 0 || dialogPaints != 0 {
		t.Fatal("the hook ran before any View")
	}
	f.NaturalSize()
	f.Render()
	if formPaints != 0 {
		t.Error("NaturalSize or Render ran the hook; only View paints")
	}
	for range 3 {
		f.View()
		d.View()
		sendKeys(f, "a")
	}
	if formPaints != 1 || dialogPaints != 1 {
		t.Errorf("hook ran form=%d dialog=%d times, want 1 each", formPaints, dialogPaints)
	}
}

// TestTextAreaMultipleLines: alt+enter and ctrl+j insert newlines, and
// enter still leaves the field, as in huh's text field.
func TestTextAreaMultipleLines(t *testing.T) {
	desc := NewTextArea("desc", "Description").Placeholder("What the session should do")
	after := NewText("after", "After")
	f := NewForm(theme.Dark(), colorprofile.TrueColor, desc, after)
	sendKeys(f, "a", "ctrl+j", "b", "alt+enter", "c")
	if desc.Get() != "a\nb\nc" {
		t.Errorf("value = %q, want three lines", desc.Get())
	}
	if f.Focused() != 0 {
		t.Fatal("a newline key moved the focus")
	}
	frame := stripANSI(f.Render())
	for _, want := range []string{"▌ a", "▌ b", "▌ c"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	cols, rows := f.NaturalSize()
	assertNaturalFrame(t, f.Render(), cols, rows)
	sendKeys(f, "enter")
	if f.Focused() != 1 {
		t.Error("enter should move past the text area")
	}
}

// TestOnChangeSetOptions is the provider → mode case. Load-bearing: the
// dependent select's cursor (on its second option) is clamped when the new
// list has one option, and the kept value survives a switch back.
func TestOnChangeSetOptions(t *testing.T) {
	both := []Option{{"Interactive", "interactive"}, {"Autonomous", "autonomous"}}
	mode := NewSelect("mode", "Mode").Options(both...).Default("autonomous")
	changes := 0
	provider := NewSelect("provider", "Provider").Options(
		Option{"Claude", "claude"}, Option{"Codex", "codex"},
	).OnChange(func(v any) tea.Cmd {
		changes++
		if v == "codex" {
			mode.SetOptions(Option{"Interactive", "interactive"})
		} else {
			mode.SetOptions(both...)
		}
		return nil
	})
	f := NewForm(theme.Dark(), colorprofile.TrueColor, provider, mode)
	if mode.Get() != "autonomous" {
		t.Fatalf("mode starts at %q", mode.Get())
	}

	sendKeys(f, "down")
	if changes != 1 || provider.Get() != "codex" {
		t.Fatalf("changes=%d provider=%q", changes, provider.Get())
	}
	if len(mode.options) != 1 || mode.cursor != 0 || mode.Get() != "interactive" {
		t.Errorf("mode options=%v cursor=%d value=%q, want the one option chosen", mode.options, mode.cursor, mode.Get())
	}

	sendKeys(f, "up")
	if changes != 2 || len(mode.options) != 2 || mode.Get() != "interactive" {
		t.Errorf("after switching back: changes=%d options=%d value=%q", changes, len(mode.options), mode.Get())
	}
	sendKeys(f, "up")
	if changes != 2 {
		t.Error("a key that did not change the value ran OnChange")
	}
}

// TestSelectFilter: typing narrows the options, j types into the filter
// instead of moving, and the choice survives the filter being cleared.
func TestSelectFilter(t *testing.T) {
	s := NewSelect("b", "Branch").Filterable().Options(
		Option{"main", "main"}, Option{"feat/join", "feat/join"}, Option{"fix/x", "fix/x"},
	)
	f := NewForm(theme.Dark(), colorprofile.TrueColor, s)
	cols, rows := f.NaturalSize()
	sendKeys(f, "j")
	if s.query != "j" || s.Get() != "feat/join" {
		t.Errorf("query=%q value=%q, want j to filter to feat/join", s.query, s.Get())
	}
	sendKeys(f, "z")
	if s.Get() != "" || !strings.Contains(stripANSI(f.Render()), "no match") {
		t.Errorf("a filter matching nothing should show no match:\n%s", stripANSI(f.Render()))
	}
	if msgs := sendKeys(f, "enter"); len(msgs) != 0 {
		t.Error("submitted with nothing selected")
	}
	sendKeys(f, "backspace", "backspace")
	if s.query != "" || len(s.matches) != 3 || s.Get() != "feat/join" {
		t.Errorf("clearing the filter should restore the list and keep the choice, got %d options, %q", len(s.matches), s.Get())
	}
	assertNaturalFrame(t, f.Render(), cols, rows)
}

// TestHostsRetheme: Init asks for the background colour, and a light reply
// re-themes every field without leaving holes.
func TestHostsRetheme(t *testing.T) {
	requestType := fmt.Sprintf("%T", tea.RequestBackgroundColor())
	asks := func(cmd tea.Cmd) bool {
		for _, msg := range drain(cmd) {
			if fmt.Sprintf("%T", msg) == requestType {
				return true
			}
		}
		return false
	}
	white := tea.BackgroundColorMsg{Color: color.White}

	f, name, _, _ := sessionForm()
	if !asks(f.Init()) {
		t.Error("Form.Init does not request the background colour")
	}
	f.Update(white)
	if f.Theme().Surface != theme.Light().Surface || name.theme.Surface != theme.Light().Surface {
		t.Error("a light background did not re-theme the form and its fields")
	}
	cols, rows := f.NaturalSize()
	assertNaturalFrame(t, f.Render(), cols, rows)

	d := NewDialog(theme.Dark(), colorprofile.TrueColor, "t", []string{"b"})
	if !asks(d.Init()) {
		t.Error("Dialog.Init does not request the background colour")
	}
	d.Update(white)
	if d.Theme().Surface != theme.Light().Surface {
		t.Error("a light background did not re-theme the dialog")
	}

	custom := theme.Dark()
	custom.Accent = lipgloss.Color("#ff0000")
	f.WithThemeFunc(func(color.Color) theme.Theme { return custom })
	f.Update(white)
	if f.Theme().Accent != custom.Accent {
		t.Error("WithThemeFunc was not used to pick the theme")
	}

	// List and InfoPanel: the rows that were missing. A host that never
	// asks stays dark and TrueColor for its whole life.
	l := NewList(theme.Dark(), colorprofile.TrueColor, []Row{{Label: "a"}})
	if !asks(l.Init()) {
		t.Error("List.Init does not request the background colour")
	}
	l.Update(white)
	if l.Theme().Surface != theme.Light().Surface || l.card.theme.Surface != theme.Light().Surface {
		t.Error("a light background did not re-theme the list and its card")
	}
	l.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	if l.card.profile != colorprofile.ANSI {
		t.Errorf("list card profile = %v after a ColorProfileMsg, want ANSI", l.card.profile)
	}
	l.WithThemeFunc(func(color.Color) theme.Theme { return custom })
	l.Update(white)
	if l.Theme().Accent != custom.Accent {
		t.Error("List.WithThemeFunc was not used to pick the theme")
	}

	p := NewInfoPanel(theme.Dark(), colorprofile.TrueColor, "T", func(int) []string { return []string{"x"} })
	if !asks(p.Init()) {
		t.Error("InfoPanel.Init does not request the background colour")
	}
	p.Update(white)
	if p.Theme().Surface != theme.Light().Surface || p.card.theme.Surface != theme.Light().Surface {
		t.Error("a light background did not re-theme the info panel and its card")
	}
	p.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	if p.card.profile != colorprofile.ANSI {
		t.Errorf("info card profile = %v after a ColorProfileMsg, want ANSI", p.card.profile)
	}
	p.WithThemeFunc(func(color.Color) theme.Theme { return custom })
	p.Update(white)
	if p.Theme().Accent != custom.Accent {
		t.Error("InfoPanel.WithThemeFunc was not used to pick the theme")
	}
}
