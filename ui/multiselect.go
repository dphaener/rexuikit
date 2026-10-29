package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/theme"
)

// multiSelectSep joins a MultiSelect's chosen values in Value. A Form
// compares a field's value before and after each key with ==, which panics on
// a slice, so Value must be comparable; the separator is a control character
// no option value is expected to contain.
const multiSelectSep = "\x1f"

// MultiSelect is a vertical checklist: space toggles the row under the
// cursor, up/down (and j/k unless the list is filterable) move it, enter
// goes through the form like every other field, and an optional filter
// narrows the rows as the user types.
//
// The cursor, filter and scroll behaviour is a Select's: a MultiSelect
// drives one for its movement, so the two cannot drift apart. A choice
// belongs to an option value, not to a row, so it survives the filter hiding
// the row and a SetOptions that still has the value.
type MultiSelect struct {
	fieldBase
	list   *Select
	chosen map[string]bool
	min    int
}

// NewMultiSelect returns a checklist whose chosen values are stored under key
// in the form's Values, titled label.
func NewMultiSelect(key, label string) *MultiSelect {
	m := &MultiSelect{
		fieldBase: fieldBase{key: key, label: label},
		list:      NewSelect(key, label),
		chosen:    map[string]bool{},
	}
	m.theme = theme.Dark()
	return m
}

// Options sets the options. Values chosen by Default stay chosen.
func (m *MultiSelect) Options(opts ...Option) *MultiSelect { m.list.Options(opts...); return m }

// Default chooses the options with these values, replacing any earlier
// choice. A value no option has is ignored by Get.
func (m *MultiSelect) Default(values ...string) *MultiSelect {
	m.chosen = make(map[string]bool, len(values))
	for _, v := range values {
		m.chosen[v] = true
	}
	return m
}

// Rows fixes the number of visible option rows, as Select.Rows does.
func (m *MultiSelect) Rows(n int) *MultiSelect { m.list.Rows(n); return m }

// Filterable adds a filter line, as Select.Filterable does. Space still
// toggles, so a filter cannot contain a space.
func (m *MultiSelect) Filterable() *MultiSelect { m.list.Filterable(); return m }

// Min requires at least n chosen options before the field validates; 0 (the
// default) allows none.
func (m *MultiSelect) Min(n int) *MultiSelect { m.min = max(n, 0); return m }

// OnChange works as Text.OnChange. It runs when a toggle changes the chosen
// set.
func (m *MultiSelect) OnChange(fn func(value any) tea.Cmd) *MultiSelect { m.onChange = fn; return m }

// SetOptions replaces the options, as Select.SetOptions does. Chosen values
// the new options lack drop out of Get. It does not run OnChange.
func (m *MultiSelect) SetOptions(opts ...Option) { m.list.SetOptions(opts...) }

// Get is the chosen options' values, in option order. It is never nil.
func (m *MultiSelect) Get() []string {
	out := []string{}
	for _, o := range m.list.options {
		if m.chosen[o.Value] && !slices.Contains(out, o.Value) {
			out = append(out, o.Value)
		}
	}
	return out
}

// Value is the chosen values joined by an unprintable separator, in option
// order: a string, because the form compares values with ==, which a slice
// would panic. Read the typed result with Get, or Values.Strings from a
// submitted form.
func (m *MultiSelect) Value() any { return strings.Join(m.Get(), multiSelectSep) }

// Strings is the chosen values of a MultiSelect field, or nil.
func (v Values) Strings(key string) []string {
	s, _ := v[key].(string)
	if s == "" {
		return nil
	}
	return strings.Split(s, multiSelectSep)
}

func (m *MultiSelect) NaturalSize() (cols, rows int) { return fieldNaturalSize(m) }

func (m *MultiSelect) init() tea.Cmd  { return nil }
func (m *MultiSelect) focus() tea.Cmd { return nil }
func (m *MultiSelect) blur()          {}

func (m *MultiSelect) validate() error {
	if n := len(m.Get()); n < m.min {
		m.err = fmt.Errorf("choose at least %d", m.min)
	} else {
		m.err = nil
	}
	return m.err
}

func (m *MultiSelect) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if s := k.String(); k.Code == tea.KeySpace || s == " " || s == "space" {
		m.toggle()
		return nil
	}
	m.list.key(k)
	return nil
}

// toggle flips the row under the cursor and clears a shown error once the
// choice satisfies the minimum, so the message does not outlive its cause.
func (m *MultiSelect) toggle() {
	o, ok := m.list.Selected()
	if !ok {
		return
	}
	if m.chosen[o.Value] {
		delete(m.chosen, o.Value)
	} else {
		m.chosen[o.Value] = true
	}
	if m.err != nil && len(m.Get()) >= m.min {
		m.err = nil
	}
}

func (m *MultiSelect) naturalContent() int {
	w := lipgloss.Width("no options")
	for _, o := range m.list.options {
		w = max(w, lipgloss.Width("› [x] "+o.Label))
	}
	if m.list.filterable {
		w = max(w, lipgloss.Width("› type to filter"))
	}
	return w
}

func (m *MultiSelect) wellLines(width int, focused bool) []string {
	t := m.theme
	text := t.On(t.Text, t.Well)
	muted := t.On(t.Muted, t.Well)
	s := m.list
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
	case len(s.matches) == 0 && s.query != "":
		lines = append(lines, muted.Render("no match"))
	case len(s.matches) == 0:
		lines = append(lines, muted.Render("no options"))
	default:
		end := min(s.offset+n, len(s.matches))
		for pos := s.offset; pos < end; pos++ {
			o := s.options[s.matches[pos]]
			label := xansi.Truncate(o.Label, max(width-6, 1), "…")
			box, on := "[ ] ", m.chosen[o.Value]
			if on {
				box = "[x] "
			}
			if pos != s.cursor {
				lines = append(lines, text.Render("  "+box+label))
				continue
			}
			marker := muted.Render("› ")
			chosen := text
			if focused {
				marker = t.On(t.Accent, t.Well).Render("› ")
				chosen = t.On(t.Accent, t.Well).Bold(true)
			}
			lines = append(lines, marker+chosen.Render(box+label))
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

func (m *MultiSelect) retheme(t theme.Theme) { m.theme = t }
