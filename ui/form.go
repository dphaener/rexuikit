package ui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

const (
	formHintNext   = "enter next · shift+tab back · esc cancel"
	formHintSubmit = "enter submit · shift+tab back · esc cancel"
)

// kitHost is the state every component host shares: the theme and how to
// re-derive it, the colour profile, the window size and the first-paint hook.
type kitHost struct {
	theme        theme.Theme
	themeFor     func(color.Color) theme.Theme
	profile      colorprofile.Profile
	w, h         int
	onFirstPaint func()
	painted      bool
}

func newKitHost(t theme.Theme, profile colorprofile.Profile) kitHost {
	return kitHost{theme: t, themeFor: theme.FromBackground, profile: profile}
}

func (h *kitHost) handle(msg tea.Msg) (handled, rethemed bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.w, h.h = msg.Width, msg.Height
		return true, false
	case tea.BackgroundColorMsg:
		h.theme = h.themeFor(msg.Color)
		return true, true
	case tea.ColorProfileMsg:
		h.profile = msg.Profile
		return true, false
	}
	return false, false
}

// markPainted runs the first-paint hook once, for the first non-empty frame.
// It runs from View, after the frame string is built and before bubbletea
// writes it: the closest a model can get to "on screen".
func (h *kitHost) markPainted(frame string) {
	if h.painted || frame == "" {
		return
	}
	h.painted = true
	if h.onFirstPaint != nil {
		h.onFirstPaint()
	}
}

// FormStatus is where a Form is in its life.
type FormStatus int

const (
	FormPending FormStatus = iota
	FormSubmitted
	FormCancelled
)

type FormSubmittedMsg struct {
	Values Values
}

// FormCancelledMsg is sent when the user presses esc or ctrl+c. As with
// ListCancelledMsg the host decides what that means; a Form never quits the
// program itself, so it can be embedded in a larger model.
type FormCancelledMsg struct{}

type Values map[string]any

// String is the value of a Text, TextArea or Select field, or "".
func (v Values) String(key string) string { s, _ := v[key].(string); return s }

// Bool is the value of a Confirm field, or false.
func (v Values) Bool(key string) bool { b, _ := v[key].(bool); return b }

// Form hosts fields on one Card: every field visible at once, enter or tab
// validates the focused field and moves on, shift+tab goes back, enter on the
// last field validates everything and submits, esc cancels.
//
// It is a bubbletea model. Its size comes from tea.WindowSizeMsg; before one
// arrives it draws at NaturalSize, which is also what a surface should size
// the Rex layer to.
type Form struct {
	kitHost
	fields   []Field
	focus    int
	status   FormStatus
	subtitle string
	hint     string
}

// NewForm returns a form over fields in theme t, which is the first-frame
// theme until a BackgroundColorMsg re-themes it. profile is the terminal's
// colour profile (see NewCard). Field keys must be unique.
func NewForm(t theme.Theme, profile colorprofile.Profile, fields ...Field) *Form {
	seen := map[string]bool{}
	for _, fld := range fields {
		if seen[fld.Key()] {
			panic(fmt.Sprintf("ui.NewForm: duplicate field key %q", fld.Key()))
		}
		seen[fld.Key()] = true
		fld.retheme(t)
	}
	if len(fields) > 0 {
		// Focused now, not only in Init, so a form driven without a program
		// (a test, a host that forwards keys before Init's commands run)
		// already accepts typing. Init returns the focus command again.
		fields[0].focus()
	}
	return &Form{kitHost: newKitHost(t, profile), fields: fields}
}

// WithSubtitle sets the card's in-content subtitle.
func (f *Form) WithSubtitle(s string) *Form { f.subtitle = s; return f }

// WithHint replaces the default hint bar, which names enter as "next" or
// "submit" depending on the focused field.
func (f *Form) WithHint(s string) *Form { f.hint = s; return f }

// OnFirstPaint sets a hook run once, after the first non-empty View.
func (f *Form) OnFirstPaint(fn func()) *Form { f.onFirstPaint = fn; return f }

// WithThemeFunc replaces theme.FromBackground as the way a BackgroundColorMsg
// picks the theme, for callers mapping their own palette onto the tokens.
func (f *Form) WithThemeFunc(fn func(color.Color) theme.Theme) *Form { f.themeFor = fn; return f }

func (f *Form) Theme() theme.Theme { return f.theme }

// Status reports whether the form is still open, submitted or cancelled.
func (f *Form) Status() FormStatus { return f.status }

func (f *Form) Focused() int { return f.focus }

func (f *Form) Values() Values {
	v := make(Values, len(f.fields))
	for _, fld := range f.fields {
		v[fld.Key()] = fld.Value()
	}
	return v
}

// Init asks the terminal for its background colour (bubbletea sends
// BackgroundColorMsg only on request), focuses the first field and starts
// every Select's loader.
func (f *Form) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	if len(f.fields) > 0 {
		cmds = append(cmds, f.fields[0].focus())
	}
	for _, fld := range f.fields {
		cmds = append(cmds, fld.init())
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model. Keys and pastes go to the focused field; any
// other message goes to every field, which is how an OptionsMsg reaches its
// Select wherever the focus is.
func (f *Form) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, rethemed := f.handle(msg); handled {
		if rethemed {
			for _, fld := range f.fields {
				fld.retheme(f.theme)
			}
		}
		return f, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if f.status != FormPending {
			return f, nil
		}
		switch msg.String() {
		case "esc", "ctrl+c":
			f.status = FormCancelled
			return f, func() tea.Msg { return FormCancelledMsg{} }
		case "enter", "tab":
			return f, f.next()
		case "shift+tab":
			return f, f.moveTo(f.focus - 1)
		}
		return f, f.forward(f.current(), msg)
	case tea.PasteMsg:
		return f, f.forward(f.current(), msg)
	}
	cmds := make([]tea.Cmd, 0, len(f.fields))
	for _, fld := range f.fields {
		cmds = append(cmds, f.forward(fld, msg))
	}
	return f, tea.Batch(cmds...)
}

func (f *Form) current() Field {
	if len(f.fields) == 0 {
		return nil
	}
	return f.fields[f.focus]
}

// forward updates one field and runs its OnChange hook when the update
// changed its value. Values are strings or bools, so == is the comparison.
func (f *Form) forward(fld Field, msg tea.Msg) tea.Cmd {
	if fld == nil {
		return nil
	}
	before := fld.Value()
	cmd := fld.update(msg)
	if after := fld.Value(); after != before {
		if hook := fld.base().onChange; hook != nil {
			cmd = tea.Batch(cmd, hook(after))
		}
	}
	return cmd
}

func (f *Form) next() tea.Cmd {
	if fld := f.current(); fld != nil && fld.validate() != nil {
		return nil
	}
	if f.focus < len(f.fields)-1 {
		return f.moveTo(f.focus + 1)
	}
	return f.submit()
}

// submit re-validates every field, since an earlier field's answer can go
// stale (an OnChange emptied a Select, a loader failed after the user left
// it), and focuses the first invalid one instead of submitting.
func (f *Form) submit() tea.Cmd {
	for i, fld := range f.fields {
		if fld.validate() != nil {
			return f.moveTo(i)
		}
	}
	f.status = FormSubmitted
	values := f.Values()
	return func() tea.Msg { return FormSubmittedMsg{Values: values} }
}

func (f *Form) moveTo(i int) tea.Cmd {
	if i < 0 || i >= len(f.fields) || i == f.focus {
		return nil
	}
	f.fields[f.focus].blur()
	f.focus = i
	return f.fields[i].focus()
}

func (f *Form) fieldWidth(w int) int {
	if w > 0 {
		return max(w-2*cardPadCols, 1)
	}
	width := 1
	for _, fld := range f.fields {
		cols, _ := fld.NaturalSize()
		width = max(width, cols)
	}
	return width
}

func (f *Form) content(width int) string {
	parts := make([]string, len(f.fields))
	for i, fld := range f.fields {
		parts[i] = renderField(fld, width, i == f.focus && f.status == FormPending, f.profile)
	}
	return strings.Join(parts, "\n")
}

func (f *Form) card(hint string) Card {
	c := NewCard(f.theme, f.profile).WithHint(hint)
	if f.subtitle != "" {
		c = c.WithSubtitle(f.subtitle)
	}
	return c
}

func (f *Form) currentHint() string {
	switch {
	case f.hint != "":
		return f.hint
	case f.focus == len(f.fields)-1:
		return formHintSubmit
	default:
		return formHintNext
	}
}

// NaturalSize is the form's size in cells with every field at its natural
// width and the wider of the two default hints, so the size does not change
// as the focus moves.
func (f *Form) NaturalSize() (cols, rows int) {
	hint := f.hint
	if hint == "" {
		hint = formHintNext
		if lipgloss.Width(formHintSubmit) > lipgloss.Width(hint) {
			hint = formHintSubmit
		}
	}
	return f.card(hint).NaturalSize(f.content(f.fieldWidth(0)))
}

// Render draws the form at the window size, or at NaturalSize before the
// first WindowSizeMsg.
func (f *Form) Render() string {
	w, h := f.w, f.h
	if w <= 0 || h <= 0 {
		nw, nh := f.NaturalSize()
		if w <= 0 {
			w = nw
		}
		if h <= 0 {
			h = nh
		}
	}
	frame := f.card(f.currentHint()).Render(f.content(f.fieldWidth(w)), w, h)
	return paint.Fill(frame, f.theme.Surface, f.profile)
}

func (f *Form) View() tea.View {
	frame := f.Render()
	defer f.markPainted(frame)
	return tea.NewView(frame)
}
