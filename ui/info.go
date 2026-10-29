package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

// Layout constants for InfoPanel. infoNaturalWidth is the width the body is
// asked to render at when sizing the layer before any WindowSizeMsg;
// infoMaxRows caps the natural height the same way listMaxRows does.
const (
	infoNaturalWidth = 76
	infoMaxRows      = 20
	infoHint         = "↑↓ scroll · esc close"
)

// InfoClosedMsg is sent when the user presses esc or q. As with
// ListCancelledMsg the host decides what closing means.
type InfoClosedMsg struct{}

// infoTickMsg is the refresh timer firing.
type infoTickMsg struct{}

// InfoPanel is a scrollable, painted viewer over caller-rendered lines.
//
// Body receives the usable column count and returns pre-rendered lines; the
// panel never styles or wraps them, so a caller (forge's quick info) keeps its
// own panel colours. Cells the lines leave without a background are closed by
// the Card's fill pass. With a refresh interval set the panel calls Body
// again on every tick and keeps the scroll position, clamped when the new body
// is shorter.
type InfoPanel struct {
	theme    theme.Theme
	themeFor func(color.Color) theme.Theme
	profile  colorprofile.Profile
	card     Card
	title    string
	hint     string
	body     func(width int) []string
	refresh  time.Duration

	lines     []string
	bodyWidth int
	offset    int
	w, h      int

	onFirstPaint func()
	painted      bool
}

// NewInfoPanel returns a viewer in theme t showing body's lines under title.
// An empty title omits the title row.
func NewInfoPanel(t theme.Theme, profile colorprofile.Profile, title string, body func(width int) []string) *InfoPanel {
	p := &InfoPanel{
		theme:    t,
		themeFor: theme.FromBackground,
		profile:  profile,
		card:     NewCard(t, profile),
		title:    title,
		hint:     infoHint,
		body:     body,
	}
	p.rebuild(infoNaturalWidth)
	return p
}

// WithHint replaces the hint line. The scroll position is appended to it when
// the body is taller than the window.
func (p *InfoPanel) WithHint(s string) *InfoPanel { p.hint = s; return p }

// WithThemeFunc replaces theme.FromBackground as the way a
// BackgroundColorMsg picks the theme, as Form.WithThemeFunc.
func (p *InfoPanel) WithThemeFunc(fn func(color.Color) theme.Theme) *InfoPanel {
	p.themeFor = fn
	return p
}

// Theme is the panel's current theme.
func (p *InfoPanel) Theme() theme.Theme { return p.theme }

// WithRefresh makes the panel call Body again every d. Zero or less disables
// the tick.
func (p *InfoPanel) WithRefresh(d time.Duration) *InfoPanel { p.refresh = d; return p }

// OnFirstPaint registers fn to run once, right after the first View that
// produced a frame (see List.OnFirstPaint).
func (p *InfoPanel) OnFirstPaint(fn func()) *InfoPanel { p.onFirstPaint = fn; return p }

// Rebuild calls Body again at the current width and keeps the scroll
// position, clamped when the new body is shorter: what the refresh tick
// does, for a host that learns of new data on its own schedule.
func (p *InfoPanel) Rebuild() { p.rebuild(p.bodyWidth) }

// rebuild re-renders the body at width columns and keeps the scroll offset
// legal for the new line count.
func (p *InfoPanel) rebuild(width int) {
	p.bodyWidth = width
	p.lines = p.body(width)
	p.offset = min(p.offset, p.maxOffset())
}

// NaturalSize is the smallest frame that shows the title, up to twenty body
// lines and the hint without truncation.
func (p *InfoPanel) NaturalSize() (cols, rows int) {
	shown := p.lines[:min(len(p.lines), infoMaxRows)]
	return p.cardFor(0, len(shown)).NaturalSize(p.content(shown))
}

// visibleRows is how many body lines fit the frame height. Before the first
// WindowSizeMsg it is the natural window.
func (p *InfoPanel) visibleRows() int {
	if p.h <= 0 {
		return max(min(len(p.lines), infoMaxRows), 1)
	}
	chrome := cardPadRows + 2 // padding, hint + spacer
	if p.title != "" {
		chrome += 2
	}
	return max(p.h-chrome, 1)
}

func (p *InfoPanel) maxOffset() int { return max(len(p.lines)-p.visibleRows(), 0) }

// cardFor is the card with the hint for a window of n body lines.
func (p *InfoPanel) cardFor(from, n int) Card {
	hint := p.hint
	if len(p.lines) > n {
		hint += fmt.Sprintf(" · %d-%d/%d", from+1, from+n, len(p.lines))
	}
	return p.card.WithHint(hint)
}

// content is the title block followed by the given body lines.
func (p *InfoPanel) content(shown []string) string {
	var out []string
	if p.title != "" {
		out = append(out, p.theme.On(p.theme.Accent, p.theme.Surface).Bold(true).Render(p.title), "")
	}
	return strings.Join(append(out, shown...), "\n")
}

// Init asks the terminal for its background colour (bubbletea sends
// BackgroundColorMsg only on request) and starts the refresh tick.
func (p *InfoPanel) Init() tea.Cmd { return tea.Batch(tea.RequestBackgroundColor, p.tick()) }

func (p *InfoPanel) tick() tea.Cmd {
	if p.refresh <= 0 {
		return nil
	}
	return tea.Tick(p.refresh, func(time.Time) tea.Msg { return infoTickMsg{} })
}

// Update implements tea.Model. A BackgroundColorMsg re-themes the panel's
// surface, title and hint and a ColorProfileMsg sets the profile its fill
// uses, as Form and Dialog do. The body's lines are the caller's and keep
// their own colours.
func (p *InfoPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		p.theme = p.themeFor(msg.Color)
		p.card = NewCard(p.theme, p.profile)
	case tea.ColorProfileMsg:
		p.profile = msg.Profile
		p.card = NewCard(p.theme, p.profile)
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		if bw := max(p.w-2*cardPadCols, 1); bw != p.bodyWidth {
			p.rebuild(bw)
		}
		p.offset = min(p.offset, p.maxOffset())
	case infoTickMsg:
		p.rebuild(p.bodyWidth)
		return p, p.tick()
	case tea.KeyPressMsg:
		return p, p.key(msg)
	}
	return p, nil
}

func (p *InfoPanel) key(msg tea.KeyPressMsg) tea.Cmd {
	page := p.visibleRows()
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		return func() tea.Msg { return InfoClosedMsg{} }
	case "up", "k":
		p.scroll(-1)
	case "down", "j":
		p.scroll(1)
	case "pgup", "ctrl+u", "b":
		p.scroll(-page)
	case "pgdown", "ctrl+d", "space", "f":
		p.scroll(page)
	case "home", "g":
		p.offset = 0
	case "end", "G":
		p.offset = p.maxOffset()
	}
	return nil
}

func (p *InfoPanel) scroll(delta int) {
	p.offset = min(max(p.offset+delta, 0), p.maxOffset())
}

// View implements tea.Model.
func (p *InfoPanel) View() tea.View {
	n := p.visibleRows()
	end := min(p.offset+n, len(p.lines))
	shown := p.lines[p.offset:end]
	card := p.cardFor(p.offset, len(shown))
	content := p.content(shown)

	w, h := p.w, p.h
	if w <= 0 || h <= 0 {
		nw, nh := card.NaturalSize(content)
		if w <= 0 {
			w = nw
		}
		if h <= 0 {
			h = nh
		}
	}
	frame := card.Render(content, w, h)
	if lipgloss.Height(frame) == 0 {
		return tea.NewView(frame)
	}
	if !p.painted {
		p.painted = true
		if p.onFirstPaint != nil {
			defer p.onFirstPaint()
		}
	}
	return tea.NewView(frame)
}
