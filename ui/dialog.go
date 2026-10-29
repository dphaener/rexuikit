package ui

import (
	"image/color"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/paint"
	"github.com/dphaener/rexuikit/ui/theme"
)

// Dialog is a solid card holding a title, pre-rendered body lines and a hint
// bar, for confirmations whose keys are not form keys (a staged `d d`, a
// mode-registry key). It routes no keys: the caller's model reads them and
// decides, and passes every other message to Update for size and theme.
type Dialog struct {
	kitHost
	title string
	body  []string
	hint  string
}

// NewDialog returns a dialog in theme t; profile is the terminal's colour
// profile (see NewCard). body lines may carry their own styling; spans that
// set no background get the Surface colour from the card's fill.
func NewDialog(t theme.Theme, profile colorprofile.Profile, title string, body []string) *Dialog {
	return &Dialog{kitHost: newKitHost(t, profile), title: title, body: slices.Clone(body)}
}

func (d *Dialog) WithHint(s string) *Dialog { d.hint = s; return d }

// OnFirstPaint sets a hook run once, after the first non-empty View.
func (d *Dialog) OnFirstPaint(fn func()) *Dialog { d.onFirstPaint = fn; return d }

// WithThemeFunc works as Form.WithThemeFunc.
func (d *Dialog) WithThemeFunc(fn func(color.Color) theme.Theme) *Dialog { d.themeFor = fn; return d }

// SetBody replaces the body lines, for a dialog whose text changes between
// stages of the caller's key machine.
func (d *Dialog) SetBody(lines []string) { d.body = slices.Clone(lines) }

func (d *Dialog) SetHint(s string) { d.hint = s }

// Theme is the dialog's current theme, for rendering body lines to match.
func (d *Dialog) Theme() theme.Theme { return d.theme }

// Init asks the terminal for its background colour; bubbletea sends
// BackgroundColorMsg only on request.
func (d *Dialog) Init() tea.Cmd { return tea.RequestBackgroundColor }

// Update applies window size, background colour and colour profile messages
// and ignores everything else, keys included.
func (d *Dialog) Update(msg tea.Msg) tea.Cmd {
	d.handle(msg)
	return nil
}

func (d *Dialog) content() string {
	t := d.theme
	var lines []string
	if d.title != "" {
		lines = append(lines, t.On(t.Text, t.Surface).Bold(true).Render(d.title))
		if len(d.body) > 0 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, d.body...)
	return strings.Join(lines, "\n")
}

func (d *Dialog) card() Card { return NewCard(d.theme, d.profile).WithHint(d.hint) }

// NaturalSize is the smallest frame showing the title, body and hint uncut.
func (d *Dialog) NaturalSize() (cols, rows int) { return d.card().NaturalSize(d.content()) }

// View draws the dialog at the window size, or at NaturalSize before the
// first WindowSizeMsg.
func (d *Dialog) View() string {
	w, h := d.w, d.h
	if w <= 0 || h <= 0 {
		nw, nh := d.NaturalSize()
		if w <= 0 {
			w = nw
		}
		if h <= 0 {
			h = nh
		}
	}
	frame := paint.Fill(d.card().Render(d.content(), w, h), d.theme.Surface, d.profile)
	d.markPainted(frame)
	return frame
}
