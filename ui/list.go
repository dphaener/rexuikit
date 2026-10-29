package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/theme"
)

// Layout constants for List. listMaxRows caps the natural height so a long
// list asks Rex for a scrollable window instead of a layer taller than the
// screen; listFilterMin keeps the filter line usable on a short list.
const (
	listMaxRows   = 10
	listFilterMin = 24
	listHint      = "↑↓ move · enter select · esc cancel"
)

// Row is one choice in a List. Detail is optional secondary text drawn after
// the label in the Muted colour.
type Row struct {
	Label  string
	Detail string
}

// ListSelectedMsg is sent when the user presses enter on a row. Index is the
// row's position in the slice given to NewList, not in the filtered view.
type ListSelectedMsg struct {
	Index int
	Row   Row
}

// ListCancelledMsg is sent when the user presses esc. The host decides what
// to do about it (close the surface, quit the program): a component that
// called tea.Quit itself could not be embedded in a larger model.
type ListCancelledMsg struct{}

// List is a filterable picker: typing narrows the rows by case-insensitive
// substring, up/down move the cursor, enter selects and esc cancels.
//
// It is a bubbletea model that fills its whole frame through a borderless
// Card, so it is solid inside a Rex layer. Its natural size is computed from
// all rows and never from the current filter, so the layer a surface opens
// does not need to resize while the user types.
type List struct {
	theme    theme.Theme
	themeFor func(color.Color) theme.Theme
	profile  colorprofile.Profile
	card     Card
	subtitle string
	rows     []Row

	query   string
	matches []int // indices into rows that pass the filter
	cursor  int   // index into matches
	offset  int   // first visible index into matches

	w, h int

	onFirstPaint func()
	painted      bool
}

// NewList returns a picker over rows in theme t. profile is the terminal's
// colour profile (see NewCard).
func NewList(t theme.Theme, profile colorprofile.Profile, rows []Row) *List {
	l := &List{
		theme:    t,
		themeFor: theme.FromBackground,
		profile:  profile,
		rows:     rows,
	}
	l.restyle()
	l.refilter()
	return l
}

// restyle rebuilds the card from the current theme, profile and title.
func (l *List) restyle() {
	l.card = NewCard(l.theme, l.profile).WithHint(listHint).WithSubtitle(l.subtitle)
}

// WithTitle sets a Muted line above the filter, such as "Base branch".
func (l *List) WithTitle(s string) *List {
	l.subtitle = s
	l.restyle()
	return l
}

// WithThemeFunc replaces theme.FromBackground as the way a
// BackgroundColorMsg picks the theme, as Form.WithThemeFunc.
func (l *List) WithThemeFunc(fn func(color.Color) theme.Theme) *List { l.themeFor = fn; return l }

// Theme is the picker's current theme.
func (l *List) Theme() theme.Theme { return l.theme }

// OnFirstPaint registers fn to run once, right after the first View that
// produced a frame. It is how a host learns the picker is on screen (to
// record time to first paint or start deferred work) without owning the
// frame loop.
func (l *List) OnFirstPaint(fn func()) *List {
	l.onFirstPaint = fn
	return l
}

// Selected reports the row under the cursor, if any row passes the filter.
func (l *List) Selected() (Row, bool) {
	if len(l.matches) == 0 {
		return Row{}, false
	}
	return l.rows[l.matches[l.cursor]], true
}

// Query is the current filter text.
func (l *List) Query() string { return l.query }

// NaturalSize is the smallest frame that shows the filter line, up to ten
// rows, the optional title and the hint bar without truncation. It does not
// change as the filter narrows.
func (l *List) NaturalSize() (cols, rows int) {
	lines := []string{strings.Repeat("x", listFilterMin), ""}
	if len(l.rows) == 0 {
		lines = append(lines, l.emptyText())
	}
	for _, r := range l.rows[:min(len(l.rows), listMaxRows)] {
		lines = append(lines, listRowText(r))
	}
	return l.card.NaturalSize(strings.Join(lines, "\n"))
}

// Init asks the terminal for its background colour; bubbletea sends
// BackgroundColorMsg only on request.
func (l *List) Init() tea.Cmd { return tea.RequestBackgroundColor }

// Update implements tea.Model. A BackgroundColorMsg re-themes the picker and
// a ColorProfileMsg sets the profile its fill uses, as Form and Dialog do.
func (l *List) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		l.theme = l.themeFor(msg.Color)
		l.restyle()
	case tea.ColorProfileMsg:
		l.profile = msg.Profile
		l.restyle()
	case tea.WindowSizeMsg:
		l.w, l.h = msg.Width, msg.Height
		l.scrollIntoView()
	case tea.KeyPressMsg:
		return l, l.key(msg)
	}
	return l, nil
}

func (l *List) key(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		return func() tea.Msg { return ListCancelledMsg{} }
	case "enter":
		if len(l.matches) == 0 {
			return nil
		}
		i := l.matches[l.cursor]
		return func() tea.Msg { return ListSelectedMsg{Index: i, Row: l.rows[i]} }
	case "up", "ctrl+p":
		l.move(-1)
	case "down", "ctrl+n":
		l.move(1)
	case "pgup":
		l.move(-l.visibleRows())
	case "pgdown":
		l.move(l.visibleRows())
	case "home":
		l.move(-len(l.matches))
	case "end":
		l.move(len(l.matches))
	case "backspace":
		if r := []rune(l.query); len(r) > 0 {
			l.query = string(r[:len(r)-1])
			l.refilter()
		}
	case "ctrl+u":
		l.query = ""
		l.refilter()
	default:
		if msg.Text != "" && msg.Mod&^tea.ModShift == 0 {
			l.query += msg.Text
			l.refilter()
		}
	}
	return nil
}

func (l *List) move(delta int) {
	if len(l.matches) == 0 {
		return
	}
	l.cursor = min(max(l.cursor+delta, 0), len(l.matches)-1)
	l.scrollIntoView()
}

// refilter rebuilds matches for the current query and keeps the cursor on the
// same row when it still matches, else on the first match.
func (l *List) refilter() {
	var keep int = -1
	if len(l.matches) > 0 {
		keep = l.matches[l.cursor]
	}
	needle := strings.ToLower(l.query)
	l.matches = l.matches[:0]
	l.cursor = 0
	for i, r := range l.rows {
		if needle != "" && !strings.Contains(strings.ToLower(r.Label+" "+r.Detail), needle) {
			continue
		}
		if i == keep {
			l.cursor = len(l.matches)
		}
		l.matches = append(l.matches, i)
	}
	l.offset = 0
	l.scrollIntoView()
}

// visibleRows is how many list rows fit: the frame height minus the card's
// padding, optional title and hint, and the filter line with its spacer.
// Before the first WindowSizeMsg it is the natural window.
func (l *List) visibleRows() int {
	if l.h <= 0 {
		return max(min(len(l.rows), listMaxRows), 1)
	}
	chrome := cardPadRows + 2 + 2 // padding, hint + spacer, filter + spacer
	if l.subtitle != "" {
		chrome += 2
	}
	return max(l.h-chrome, 1)
}

func (l *List) scrollIntoView() {
	n := l.visibleRows()
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+n {
		l.offset = l.cursor - n + 1
	}
	l.offset = max(min(l.offset, max(len(l.matches)-n, 0)), 0)
}

func (l *List) emptyText() string {
	if len(l.rows) == 0 {
		return "Nothing to choose from"
	}
	return "No matches"
}

func listRowText(r Row) string {
	if r.Detail == "" {
		return "  " + r.Label
	}
	return "  " + r.Label + "  " + r.Detail
}

// View implements tea.Model.
func (l *List) View() tea.View {
	w, h := l.w, l.h
	if w <= 0 || h <= 0 {
		nw, nh := l.NaturalSize()
		if w <= 0 {
			w = nw
		}
		if h <= 0 {
			h = nh
		}
	}
	inner := max(w-2*cardPadCols, 1)

	lines := []string{l.filterLine(inner), ""}
	if len(l.matches) == 0 {
		lines = append(lines, l.theme.On(l.theme.Muted, l.theme.Surface).Render("  "+l.emptyText()))
	}
	end := min(l.offset+l.visibleRows(), len(l.matches))
	for pos := l.offset; pos < end; pos++ {
		lines = append(lines, l.rowLine(l.rows[l.matches[pos]], inner, pos == l.cursor))
	}

	frame := l.card.Render(strings.Join(lines, "\n"), w, h)
	if !l.painted {
		l.painted = true
		if l.onFirstPaint != nil {
			defer l.onFirstPaint()
		}
	}
	return tea.NewView(frame)
}

// filterLine is the filter input: a Well-coloured bar padded to inner
// columns, with the caret and the Muted placeholder when empty.
func (l *List) filterLine(inner int) string {
	t := l.theme
	well := t.On(t.Text, t.Well)
	var s string
	if l.query == "" {
		s = t.On(t.Accent, t.Well).Render("› ") +
			t.On(t.Muted, t.Well).Render("type to filter")
	} else {
		s = t.On(t.Accent, t.Well).Render("› ") +
			well.Render(xansi.Truncate(l.query, max(inner-3, 1), "…")) +
			t.On(t.Accent, t.Well).Render("▏")
	}
	return padStyled(s, well, inner)
}

// rowLine draws one row. The active row sits on Well with an Accent marker;
// the others stay on Surface. Each is padded to inner columns in its own
// ground so the Well bar spans the full width.
func (l *List) rowLine(r Row, inner int, active bool) string {
	t := l.theme
	ground := t.Surface
	marker, fg := "  ", t.Text
	if active {
		ground = t.Well
		marker, fg = "› ", t.Accent
	}
	label := t.On(fg, ground)
	if active {
		label = label.Bold(true)
	}
	avail := max(inner-lipgloss.Width(marker), 1)
	text := xansi.Truncate(r.Label, avail, "…")
	s := t.On(t.Accent, ground).Render(marker) + label.Render(text)
	if r.Detail != "" {
		if room := avail - lipgloss.Width(text) - 2; room > 0 {
			s += t.On(t.Muted, ground).Render("  " + xansi.Truncate(r.Detail, room, "…"))
		}
	}
	return padStyled(s, t.On(t.Text, ground), inner)
}
