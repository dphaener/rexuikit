package ui

import (
	"errors"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

// Field geometry. A field is a label row, one or more well rows and a status
// row. A well row is the focus bar, a space, the content and a space, so the
// content is fieldWellChrome columns narrower than the field.
//
// The status row is always there, blank until an error shows in it. It is
// also the gap between one field and the next, so an error appearing never
// pushes the fields below it down: the layer is sized once, to the natural
// size, and a frame that grew would lose its bottom rows to the card's cut.
const (
	fieldWellChrome = 3
	// fieldMinText is the content width a Text or TextArea asks for when
	// nothing else (placeholder, label) is wider, so an empty input is not
	// a two-column slot.
	fieldMinText = 32
	// textAreaLines is a TextArea's visible lines unless Lines says otherwise.
	textAreaLines = 3
	// selectLoaderRows is the rows an async Select reserves when Rows was
	// not called: the option count is unknown until the loader returns, and
	// the field must not change height when it does.
	selectLoaderRows = 4
)

// errLoading is a Select's validation error while its loader has not
// answered; the form cannot submit a value that does not exist yet.
var errLoading = errors.New("still loading")

// errNothingSelected is a Select's validation error when no option is
// visible: no options at all, or a filter that matches none.
var errNothingSelected = errors.New("nothing selected")

// Field is one input of a Form: Text, TextArea, Select or Confirm. The set is
// closed (the unexported methods are the rendering and focus contract the
// Form drives), so a Form can rely on every field drawing a label, a well and
// a status row.
type Field interface {
	// Key names the field's value in the form's Values.
	Key() string
	// Value is the field's current value: a string for Text, TextArea and
	// Select, a bool for Confirm.
	Value() any
	// Err is the error the field currently shows, or nil.
	Err() error
	// NaturalSize is the field's size in cells at its natural width:
	// label, well rows and the status row.
	NaturalSize() (cols, rows int)

	base() *fieldBase
	init() tea.Cmd
	focus() tea.Cmd
	blur()
	update(tea.Msg) tea.Cmd
	validate() error
	naturalContent() int
	wellLines(width int, focused bool) []string
	retheme(theme.Theme)
}

type fieldBase struct {
	key      string
	label    string
	err      error
	onChange func(value any) tea.Cmd
	theme    theme.Theme
}

func (b *fieldBase) Key() string      { return b.key }
func (b *fieldBase) Err() error       { return b.err }
func (b *fieldBase) base() *fieldBase { return b }

func fieldNaturalSize(f Field) (cols, rows int) {
	b := f.base()
	cols = max(lipgloss.Width(b.label), f.naturalContent()+fieldWellChrome)
	return cols, 1 + len(f.wellLines(f.naturalContent(), false)) + 1
}

// renderField draws f at width columns: the label (Accent and bold when
// focused, Muted otherwise), the well rows with the focus bar, and the status
// row. Each well row is filled with Well on its own before it is framed, so a
// cell a bubbles widget left without a background (its virtual cursor) takes
// the well's colour rather than the card's Surface, which would cut a notch
// into the well.
func renderField(f Field, width int, focused bool, profile colorprofile.Profile) string {
	b := f.base()
	t := b.theme
	body := t.On(t.Text, t.Surface)
	well := t.On(t.Text, t.Well)

	labelStyle := t.On(t.Muted, t.Surface)
	bar := t.On(t.Well, t.Well).Render(" ")
	if focused {
		labelStyle = t.On(t.Accent, t.Surface).Bold(true)
		bar = t.On(t.Accent, t.Well).Render("▌")
	}

	rows := []string{padStyled(labelStyle.Render(xansi.Truncate(b.label, width, "…")), body, width)}
	content := max(width-fieldWellChrome, 1)
	for _, line := range f.wellLines(content, focused) {
		inner := padStyled(xansi.Truncate(line, content, ""), well, content)
		inner = padStyled(paint.Fill(inner, t.Well, profile), well, content)
		rows = append(rows, bar+well.Render(" ")+inner+well.Render(" "))
	}

	status := padStyled("", body, width)
	if err := f.Err(); err != nil {
		msg := xansi.Truncate("✗ "+err.Error(), width, "…")
		status = padStyled(t.On(t.Danger, t.Surface).Render(msg), body, width)
	}
	rows = append(rows, status)
	return strings.Join(rows, "\n")
}

// padStyled right-pads s with spaces in style to width columns. The padding
// is its own span: lipgloss's Width pads after the reset that ends an inner
// span, which would leave the padding on the default background.
func padStyled(s string, style lipgloss.Style, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		s += style.Render(strings.Repeat(" ", gap))
	}
	return s
}

type Text struct {
	fieldBase
	in        textinput.Model
	validator func(string) error
}

// NewText returns a one-line input whose value is stored under key in the
// form's Values, titled label.
func NewText(key, label string) *Text {
	in := textinput.New()
	in.Prompt = ""
	f := &Text{fieldBase: fieldBase{key: key, label: label}, in: in}
	f.retheme(theme.Dark())
	return f
}

// Placeholder sets the Muted text shown while the input is empty.
func (f *Text) Placeholder(s string) *Text { f.in.Placeholder = s; return f }

// Validator sets the check run when the user leaves the field (enter or tab)
// and on submit. A non-nil error keeps focus on the field and shows the
// error in the status row until the value changes.
func (f *Text) Validator(fn func(string) error) *Text { f.validator = fn; return f }

// CharLimit caps the value's length in runes; 0 means no limit.
func (f *Text) CharLimit(n int) *Text { f.in.CharLimit = n; return f }

func (f *Text) Default(s string) *Text { f.in.SetValue(s); return f }

// OnChange sets a hook run whenever the value changes through user input.
// The returned command, if any, is run by the form.
func (f *Text) OnChange(fn func(value any) tea.Cmd) *Text { f.onChange = fn; return f }

func (f *Text) Get() string { return f.in.Value() }

func (f *Text) Value() any { return f.in.Value() }

func (f *Text) NaturalSize() (cols, rows int) { return fieldNaturalSize(f) }

func (f *Text) init() tea.Cmd   { return nil }
func (f *Text) focus() tea.Cmd  { return f.in.Focus() }
func (f *Text) blur()           { f.in.Blur() }
func (f *Text) validate() error { f.err = runCheck(f.validator, f.in.Value()); return f.err }

func (f *Text) update(msg tea.Msg) tea.Cmd {
	before := f.in.Value()
	var cmd tea.Cmd
	f.in, cmd = f.in.Update(msg)
	if f.in.Value() != before {
		f.err = nil
	}
	return cmd
}

func (f *Text) naturalContent() int {
	want := fieldMinText
	if f.in.CharLimit > 0 {
		want = min(want, f.in.CharLimit+1)
	}
	// +1: the cursor sits after the last character.
	return max(want, lipgloss.Width(f.in.Placeholder)+1)
}

func (f *Text) wellLines(width int, _ bool) []string {
	// textinput draws its cursor one column past Width, so Width is one
	// less than the content columns.
	f.in.SetWidth(max(width-1, 1))
	return []string{f.in.View()}
}

func (f *Text) retheme(t theme.Theme) {
	f.theme = t
	state := textinput.StyleState{
		Text:        t.On(t.Text, t.Well),
		Placeholder: t.On(t.Muted, t.Well),
		Suggestion:  t.On(t.Muted, t.Well),
		Prompt:      t.On(t.Accent, t.Well),
	}
	f.in.SetStyles(textinput.Styles{
		Focused: state,
		Blurred: state,
		// A blinking cursor makes Focus return a tick the form would have to
		// keep alive for the life of the modal, redrawing the layer twice a
		// second for nothing; every field's cursor is a static block.
		Cursor: textinput.CursorStyle{Color: t.Accent, Shape: tea.CursorBlock, Blink: false},
	})
}

// TextArea is a multi-line input on bubbles' textarea. Enter moves to the
// next field like every other field does, so a newline is alt+enter or
// ctrl+j, the keys huh's text field uses.
type TextArea struct {
	fieldBase
	ta        textarea.Model
	lines     int
	validator func(string) error
}

// NewTextArea returns a multi-line input showing three lines.
func NewTextArea(name, label string) *TextArea {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	f := &TextArea{fieldBase: fieldBase{key: name, label: label}, ta: ta, lines: textAreaLines}
	f.ta.SetHeight(f.lines)
	f.retheme(theme.Dark())
	return f
}

// Lines sets the number of visible lines (at least 1). Longer text scrolls.
func (f *TextArea) Lines(n int) *TextArea {
	f.lines = max(n, 1)
	f.ta.SetHeight(f.lines)
	return f
}

// Placeholder sets the Muted text shown while the input is empty.
func (f *TextArea) Placeholder(s string) *TextArea { f.ta.Placeholder = s; return f }

// Validator works as Text.Validator.
func (f *TextArea) Validator(fn func(string) error) *TextArea { f.validator = fn; return f }

// CharLimit caps the value's length in runes; 0 means no limit.
func (f *TextArea) CharLimit(n int) *TextArea { f.ta.CharLimit = n; return f }

func (f *TextArea) Default(s string) *TextArea { f.ta.SetValue(s); return f }

// OnChange works as Text.OnChange.
func (f *TextArea) OnChange(fn func(value any) tea.Cmd) *TextArea { f.onChange = fn; return f }

// Get is the typed value, lines joined by "\n".
func (f *TextArea) Get() string { return f.ta.Value() }

func (f *TextArea) Value() any { return f.ta.Value() }

func (f *TextArea) NaturalSize() (cols, rows int) { return fieldNaturalSize(f) }

func (f *TextArea) init() tea.Cmd   { return nil }
func (f *TextArea) focus() tea.Cmd  { return f.ta.Focus() }
func (f *TextArea) blur()           { f.ta.Blur() }
func (f *TextArea) validate() error { f.err = runCheck(f.validator, f.ta.Value()); return f.err }

func (f *TextArea) update(msg tea.Msg) tea.Cmd {
	before := f.ta.Value()
	var cmd tea.Cmd
	f.ta, cmd = f.ta.Update(msg)
	if f.ta.Value() != before {
		f.err = nil
	}
	return cmd
}

func (f *TextArea) naturalContent() int {
	return max(fieldMinText, lipgloss.Width(f.ta.Placeholder)+1)
}

func (f *TextArea) wellLines(width int, _ bool) []string {
	f.ta.SetWidth(width)
	f.ta.SetHeight(f.lines)
	lines := strings.Split(strings.TrimSuffix(f.ta.View(), "\n"), "\n")
	// The viewport already shows exactly lines rows; normalising guards the
	// layout against a bubbles change in how it terminates the last row.
	for len(lines) < f.lines {
		lines = append(lines, "")
	}
	return lines[:f.lines]
}

func (f *TextArea) retheme(t theme.Theme) {
	f.theme = t
	well := t.On(t.Text, t.Well)
	state := textarea.StyleState{
		Base:             lipgloss.NewStyle().Background(t.Well),
		Text:             well,
		LineNumber:       t.On(t.Muted, t.Well),
		CursorLineNumber: t.On(t.Muted, t.Well),
		CursorLine:       well,
		EndOfBuffer:      t.On(t.Well, t.Well),
		Placeholder:      t.On(t.Muted, t.Well),
		Prompt:           t.On(t.Accent, t.Well),
		Selection:        t.On(t.Surface, t.Accent),
	}
	f.ta.SetStyles(textarea.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textarea.CursorStyle{Color: t.Accent, Shape: tea.CursorBlock, Blink: false},
	})
}

// Option is one choice of a Select: Label is shown, Value is returned.
type Option struct {
	Label string
	Value string
}

// Loader produces a Select's options after the first paint: the options, the
// value to preselect (ignored when no option has it), and an error that is
// shown in the field and blocks submit.
type Loader func() (options []Option, def string, err error)

// OptionsMsg delivers a Select's options. A Select with a Loader produces it
// from its Init command; a caller that marked a Select Pending sends it
// itself. A Select ignores an OptionsMsg addressed to another Select.
type OptionsMsg struct {
	Select  *Select
	Options []Option
	Default string
	Err     error
}

// Select is a vertical option list: `›` marks the chosen option, up/down (and
// j/k unless the list is filterable) move it, and an optional filter narrows
// the options as the user types.
type Select struct {
	fieldBase
	options    []Option
	matches    []int // indices into options passing the filter
	cursor     int   // index into matches
	offset     int   // first visible index into matches
	rows       int   // fixed visible rows; 0 means one per option
	filterable bool
	query      string
	def        string
	loader     Loader
	loading    bool
	loadErr    error
}

func NewSelect(key, label string) *Select {
	s := &Select{fieldBase: fieldBase{key: key, label: label}}
	s.theme = theme.Dark()
	return s
}

// Options sets the options; the Default value, if any, is preselected.
func (s *Select) Options(opts ...Option) *Select {
	s.options = slices.Clone(opts)
	s.refilter()
	s.selectValue(s.def)
	return s
}

// Default preselects the option with value v.
func (s *Select) Default(v string) *Select { s.def = v; s.selectValue(v); return s }

// Rows fixes the number of visible option rows: more options scroll, fewer
// leave blank rows. Without it the field shows every option, or reserves
// four rows while a Loader runs.
func (s *Select) Rows(n int) *Select { s.rows = max(n, 1); s.clampOffset(); return s }

// Filterable adds a filter line: typed characters narrow the options by
// case-insensitive substring, backspace and ctrl+u edit the filter. j and k
// then type into the filter, so only up/down (and ctrl+p/ctrl+n) move.
func (s *Select) Filterable() *Select { s.filterable = true; return s }

// Loader loads the options asynchronously: the field shows a loading row
// until the loader's OptionsMsg arrives, then preselects its default. The
// loader runs as a command from the form's Init, off the update loop, so a
// slow loader never delays the first frame.
func (s *Select) Loader(fn Loader) *Select { s.loader = fn; s.loading = true; return s }

// Pending shows the loading row until the caller sends an OptionsMsg for
// this Select, for callers that start loading on their own schedule.
func (s *Select) Pending() *Select { s.loading = true; return s }

// OnChange works as Text.OnChange. It also runs when an OptionsMsg changes
// the value, so a field derived from this one follows the loaded default.
func (s *Select) OnChange(fn func(value any) tea.Cmd) *Select { s.onChange = fn; return s }

// SetOptions replaces the options, typically from another field's OnChange.
// The chosen value is kept when the new options still have it; otherwise
// the cursor is clamped into the new list. It does not run OnChange, so two
// fields deriving each other's options cannot loop.
func (s *Select) SetOptions(opts ...Option) {
	prev, had := s.Selected()
	s.options = slices.Clone(opts)
	s.loading, s.loadErr = false, nil
	at := s.cursor
	s.refilter()
	s.cursor = min(at, max(len(s.matches)-1, 0))
	if had {
		s.selectValue(prev.Value)
	}
	s.clampOffset()
}

// Selected is the chosen option; false when none is visible.
func (s *Select) Selected() (Option, bool) {
	if len(s.matches) == 0 || s.loading || s.loadErr != nil {
		return Option{}, false
	}
	return s.options[s.matches[s.cursor]], true
}

// Get is the typed value: the chosen option's Value, or "".
func (s *Select) Get() string { o, _ := s.Selected(); return o.Value }

func (s *Select) Value() any { return s.Get() }

// Err implements Field. A loader error outlives edits: it stays until the
// form is cancelled.
func (s *Select) Err() error {
	if s.loadErr != nil {
		return s.loadErr
	}
	return s.err
}

func (s *Select) NaturalSize() (cols, rows int) { return fieldNaturalSize(s) }

func (s *Select) init() tea.Cmd {
	if s.loader == nil {
		return nil
	}
	load := s.loader
	return func() tea.Msg {
		opts, def, err := load()
		return OptionsMsg{Select: s, Options: opts, Default: def, Err: err}
	}
}

func (s *Select) focus() tea.Cmd { return nil }
func (s *Select) blur()          {}

func (s *Select) validate() error {
	switch {
	case s.loadErr != nil:
		return s.loadErr
	case s.loading:
		s.err = errLoading
	case len(s.matches) == 0:
		s.err = errNothingSelected
	default:
		s.err = nil
	}
	return s.err
}

func (s *Select) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case OptionsMsg:
		if msg.Select != s {
			return nil
		}
		s.loading = false
		s.err = nil
		if msg.Err != nil {
			s.loadErr = msg.Err
			return nil
		}
		s.options = slices.Clone(msg.Options)
		s.query = ""
		s.refilter()
		s.selectValue(msg.Default)
	case tea.KeyPressMsg:
		s.key(msg)
	}
	return nil
}

func (s *Select) key(msg tea.KeyPressMsg) {
	k := msg.String()
	if !s.filterable {
		switch k {
		case "k":
			k = "up"
		case "j":
			k = "down"
		}
	}
	switch k {
	case "up", "ctrl+p":
		s.move(-1)
	case "down", "ctrl+n":
		s.move(1)
	case "home":
		s.move(-len(s.matches))
	case "end":
		s.move(len(s.matches))
	case "backspace":
		if r := []rune(s.query); s.filterable && len(r) > 0 {
			s.query = string(r[:len(r)-1])
			s.refilterKeep()
		}
	case "ctrl+u":
		if s.filterable && s.query != "" {
			s.query = ""
			s.refilterKeep()
		}
	default:
		if s.filterable && msg.Text != "" && msg.Mod&^tea.ModShift == 0 {
			s.query += msg.Text
			s.refilterKeep()
		}
	}
	if len(s.matches) > 0 {
		s.err = nil
	}
}

func (s *Select) move(delta int) {
	if len(s.matches) == 0 {
		return
	}
	s.cursor = min(max(s.cursor+delta, 0), len(s.matches)-1)
	s.clampOffset()
}

func (s *Select) refilter() {
	s.matches = s.matches[:0]
	q := strings.ToLower(s.query)
	for i, o := range s.options {
		if q == "" || strings.Contains(strings.ToLower(o.Label), q) {
			s.matches = append(s.matches, i)
		}
	}
	s.cursor, s.offset = 0, 0
}

// refilterKeep refilters and keeps the chosen option chosen when it still
// matches, so typing a letter that also matches the current choice does not
// jump the cursor back to the top.
func (s *Select) refilterKeep() {
	prev, had := s.Selected()
	s.refilter()
	if had {
		s.selectValue(prev.Value)
	}
}

func (s *Select) selectValue(v string) {
	for pos, i := range s.matches {
		if s.options[i].Value == v {
			s.cursor = pos
			break
		}
	}
	s.clampOffset()
}

func (s *Select) visibleRows() int {
	switch {
	case s.rows > 0:
		return s.rows
	case s.loader != nil:
		return selectLoaderRows
	default:
		return max(len(s.options), 1)
	}
}

func (s *Select) clampOffset() {
	n := s.visibleRows()
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+n {
		s.offset = s.cursor - n + 1
	}
	s.offset = max(min(s.offset, len(s.matches)-n), 0)
}

func (s *Select) naturalContent() int {
	w := lipgloss.Width("loading…")
	for _, o := range s.options {
		w = max(w, lipgloss.Width("› "+o.Label))
	}
	if s.filterable {
		w = max(w, lipgloss.Width("› type to filter"))
	}
	return w
}

func (s *Select) wellLines(width int, focused bool) []string {
	t := s.theme
	text := t.On(t.Text, t.Well)
	muted := t.On(t.Muted, t.Well)
	n := s.visibleRows()

	var lines []string
	if s.filterable {
		if s.query == "" {
			lines = append(lines, t.On(t.Accent, t.Well).Render("› ")+muted.Render("type to filter"))
		} else {
			lines = append(lines, t.On(t.Accent, t.Well).Render("› ")+text.Render(s.query))
		}
	}

	switch {
	case s.loading:
		lines = append(lines, muted.Render("loading…"))
	case s.loadErr != nil:
		// The error is the status row; the well stays empty.
	case len(s.matches) == 0 && s.query != "":
		lines = append(lines, muted.Render("no match"))
	case len(s.matches) == 0:
		lines = append(lines, muted.Render("no options"))
	default:
		end := min(s.offset+n, len(s.matches))
		for pos := s.offset; pos < end; pos++ {
			label := xansi.Truncate(s.options[s.matches[pos]].Label, max(width-2, 1), "…")
			if pos != s.cursor {
				lines = append(lines, text.Render("  "+label))
				continue
			}
			marker := t.On(t.Muted, t.Well).Render("› ")
			chosen := text
			if focused {
				marker = t.On(t.Accent, t.Well).Render("› ")
				chosen = t.On(t.Accent, t.Well).Bold(true)
			}
			lines = append(lines, marker+chosen.Render(label))
		}
	}

	total := n
	if s.filterable {
		total++
	}
	for len(lines) < total {
		lines = append(lines, "")
	}
	return lines[:total]
}

func (s *Select) retheme(t theme.Theme) { s.theme = t }

// Confirm is a yes/no choice: left/right (h/l) toggle, y and n choose.
type Confirm struct {
	fieldBase
	value   bool
	yes, no string
}

// NewConfirm returns a yes/no choice that starts on No.
func NewConfirm(key, label string) *Confirm {
	c := &Confirm{fieldBase: fieldBase{key: key, label: label}, yes: "Yes", no: "No"}
	c.theme = theme.Dark()
	return c
}

func (c *Confirm) Labels(yes, no string) *Confirm { c.yes, c.no = yes, no; return c }

func (c *Confirm) Default(v bool) *Confirm { c.value = v; return c }

// OnChange works as Text.OnChange.
func (c *Confirm) OnChange(fn func(value any) tea.Cmd) *Confirm { c.onChange = fn; return c }

func (c *Confirm) Get() bool { return c.value }

func (c *Confirm) Value() any { return c.value }

func (c *Confirm) NaturalSize() (cols, rows int) { return fieldNaturalSize(c) }

func (c *Confirm) init() tea.Cmd   { return nil }
func (c *Confirm) focus() tea.Cmd  { return nil }
func (c *Confirm) blur()           {}
func (c *Confirm) validate() error { return nil }

func (c *Confirm) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "left", "right", "h", "l":
		c.value = !c.value
	case "y", "Y":
		c.value = true
	case "n", "N":
		c.value = false
	}
	return nil
}

func (c *Confirm) naturalContent() int {
	return lipgloss.Width(" "+c.yes+" ") + 2 + lipgloss.Width(" "+c.no+" ")
}

func (c *Confirm) wellLines(_ int, focused bool) []string {
	t := c.theme
	chosen := t.On(t.Text, t.Border).Bold(true)
	if focused {
		chosen = t.On(t.Surface, t.Accent).Bold(true)
	}
	other := t.On(t.Muted, t.Well)
	yes, no := other, other
	if c.value {
		yes = chosen
	} else {
		no = chosen
	}
	gap := t.On(t.Text, t.Well).Render("  ")
	return []string{yes.Render(" "+c.yes+" ") + gap + no.Render(" "+c.no+" ")}
}

func (c *Confirm) retheme(t theme.Theme) { c.theme = t }

func runCheck(fn func(string) error, v string) error {
	if fn == nil {
		return nil
	}
	return fn(v)
}
