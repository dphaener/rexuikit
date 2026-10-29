package surface

import (
	"errors"
	"fmt"
	"math"

	"github.com/dphaener/rexuikit/client"
)

// Layer chrome constants, copied from docs/rex-behaviors.md `## Layer
// chrome`, the single source for them. The Rex app lays a floating layer out
// in pixels: it rounds each edge to a whole pixel on its own, takes the
// border and title bar out in pixels, and cuts the rest into whole cells.
// That is why the usable size depends on where the layer sits, not only on
// its fractional width (w=0.40 over 149 columns is 57 at x=0.40 and 58 at
// x=0.55). TestChromeConstantsMatchBehaviorsDoc fails if these drift from the
// document, and the live TestLayerChromeDelta re-proves the document against
// Rex. W5.T2 injects these same values into the generated Lua.
const (
	// FramePxCols and FramePxRows turn a window's cell count into its
	// pixel extent: P = cells * cell_px + frame_px. The rows value is not a
	// whole number of cells (130 rows at 14 px span 1866 px, not 1862), so
	// the offset is in pixels.
	FramePxCols = 21
	FramePxRows = 46
	// BorderPxCols and BorderPxRows are the pixels the border and title bar
	// take out of a layer.
	BorderPxCols = 20
	BorderPxRows = 46
	// EdgeRounding is how each layer edge is rounded to a pixel, and
	// SizeRounding how the remaining pixels become cells.
	EdgeRounding = "round"
	SizeRounding = "floor"
)

// Inverse margins: the pixels BoundsForCells leaves spare beyond what the
// forward model needs, per axis. They are not chrome; rex-behaviors.md
// `### Inverse for BoundsForCells` records them and why.
//
// The rows margin was added when rows were under-determined: a centred
// 46x30 layer over 149x130 at 7x14 px (W6.T2) spanned exactly 30*14 + 44 px
// and the app gave 29 rows where the old model (1862 px, border 44) said 30.
// The review-fix re-measure (96 samples, rows swept in single pixels) pins
// rows to one model, FramePxRows and BorderPxRows above, which reproduces
// that 29. The 5 px stay because that fit, like the cols one, comes from a
// single window and cell size: they absorb a frame or border a few pixels
// off on another window. 5 px is under one row, so the layer never gets a
// row more than asked. Cols were pinned to one model from the start and
// include zero-spare samples the app honoured, so they keep none.
const (
	InverseMarginPxCols = 0
	InverseMarginPxRows = 5
)

// WindowCells is what the chrome model needs to know about a window: its
// size in cells and the size of one cell in pixels. Both come from
// com.superlogical.terminal.size of a block in the window's tiled layer.
type WindowCells struct {
	Cols, Rows int
	CellWPx    int
	CellHPx    int
}

func (w WindowCells) valid() bool {
	return w.Cols > 0 && w.Rows > 0 && w.CellWPx > 0 && w.CellHPx > 0
}

// ErrNoCellSize means a terminal size result carried no pixel cell size, so
// the chrome model has no input to work from. Guessing a cell size would
// hand the program a wrong first frame, which is what sizing exists to stop.
var ErrNoCellSize = errors.New("rexuikit/surface: terminal size has no cell pixel size")

// ErrNoTiledBlock means the window's tiled layer has no block to read the
// window's size from.
var ErrNoTiledBlock = errors.New("rexuikit/surface: window has no tiled block")

// roundEdge is EdgeRounding: half away from zero, which is floor(v+0.5) for
// the non-negative pixel values here.
func roundEdge(v float64) float64 { return math.Floor(v + 0.5) }

func axisPx(cells, framePx, cellPx int) float64 {
	return float64(cells*cellPx + framePx)
}

// cellsAxis is the model from rex-behaviors.md for one axis:
// floor((edge(pos+frac) - edge(pos) - border) / cell), which can be zero or
// negative for a layer smaller than its chrome.
func cellsAxis(cells, framePx, cellPx, borderPx int, pos, frac float64) int {
	p := axisPx(cells, framePx, cellPx)
	span := roundEdge(p*(pos+frac)) - roundEdge(p*pos)
	return int(math.Floor((span - float64(borderPx)) / float64(cellPx)))
}

// usableAxis is cellsAxis at least 1, what a program is told.
func usableAxis(cells, framePx, cellPx, borderPx int, pos, frac float64) int {
	return max(1, cellsAxis(cells, framePx, cellPx, borderPx, pos, frac))
}

// Usable is the cell size a program sees in a floating layer at bounds b
// over a window of w. It is never below 1x1: a zero or negative terminal
// size would be refused by the server and is meaningless to a program.
func Usable(w WindowCells, b client.Bounds) client.Size {
	return client.Size{
		Columns: usableAxis(w.Cols, FramePxCols, w.CellWPx, BorderPxCols, b[0], b[2]),
		Rows:    usableAxis(w.Rows, FramePxRows, w.CellHPx, BorderPxRows, b[1], b[3]),
	}
}

// Place is where a content-sized layer sits on one axis; Start is left or
// top.
type Place int

const (
	Center Place = iota
	Start
	End
)

// Anchor places a content-sized layer: H on the horizontal axis, V on the
// vertical one.
type Anchor struct {
	H, V Place
}

var (
	// Centered is the Modal anchor.
	Centered = Anchor{H: Center, V: Center}
	// RightEdge, LeftEdge, TopEdge and BottomEdge are the Drawer anchors:
	// flush against one edge, centred along it.
	RightEdge  = Anchor{H: End, V: Center}
	LeftEdge   = Anchor{H: Start, V: Center}
	TopEdge    = Anchor{H: Center, V: Start}
	BottomEdge = Anchor{H: Center, V: End}
)

// placeAxis puts a span of fraction s on one axis by p, clamped to [0, 1-s].
func placeAxis(p Place, s float64) float64 {
	var pos float64
	switch p {
	case Start:
		pos = 0
	case End:
		pos = 1 - s
	default:
		pos = (1 - s) / 2
	}
	return math.Max(0, math.Min(pos, 1-s))
}

// fitAxis is the inverse for one axis, following "### Inverse for
// BoundsForCells" in rex-behaviors.md: start from the exact pixel fraction,
// then grow one pixel at a time until the forward model gives at least want
// at the anchored position. Growing re-places the span each step because a
// centred layer's edges both move, and each edge rounds on its own. ok is
// false when the window cannot hold want; the span is then the whole axis.
//
// The check runs the forward model with marginPx added to the border, so
// the layer keeps that many pixels spare (see InverseMarginPxRows). The
// plain model then gives at least want too, since a bigger border never
// gives more cells. A window that holds want only without the margin gets
// the whole axis and ok.
func fitAxis(cells, framePx, cellPx, borderPx, marginPx, want int, p Place) (pos, span float64, ok bool) {
	px := axisPx(cells, framePx, cellPx)
	span = math.Min(1, float64(want*cellPx+borderPx+marginPx)/px)
	step := 1 / px
	for {
		pos = placeAxis(p, span)
		// cellsAxis, not usableAxis: the 1-cell floor would accept a
		// one-cell layer with no room for the margin.
		if cellsAxis(cells, framePx, cellPx, borderPx+marginPx, pos, span) >= want {
			return pos, span, true
		}
		if span >= 1 {
			// The whole axis without the margin to spare: ok still means
			// the plain model gives want, which is what Usable reports.
			return 0, 1, usableAxis(cells, framePx, cellPx, borderPx, 0, 1) >= want
		}
		span = math.Min(1, span+step)
	}
}

// BoundsForCells turns a component's natural size into fractional bounds
// over window w: the smallest layer (to one pixel) that gives the program at
// least cols x rows with the inverse margins spare, placed by anchor and
// clamped to the window. Usable of the result is then at least cols x rows
// whenever ok is true. ok is false when the window is too small on either
// axis; that axis then gets the whole window, the most the layer can have,
// and Usable reports how short it falls.
//
// The size is only guaranteed at the returned x and y: pass the bounds to
// new_layer unchanged.
func BoundsForCells(w WindowCells, cols, rows int, anchor Anchor) (b client.Bounds, ok bool) {
	cols, rows = max(1, cols), max(1, rows)
	x, sw, okC := fitAxis(w.Cols, FramePxCols, w.CellWPx, BorderPxCols, InverseMarginPxCols, cols, anchor.H)
	y, sh, okR := fitAxis(w.Rows, FramePxRows, w.CellHPx, BorderPxRows, InverseMarginPxRows, rows, anchor.V)
	return fit(client.Bounds{x, y, sw, sh}), okC && okR
}

// fit keeps bounds inside the window. The server refuses bounds that do not
// fit, and float arithmetic like (1-w)+w can land one ulp past 1.
func fit(b client.Bounds) client.Bounds {
	for _, a := range [][2]int{{0, 2}, {1, 3}} {
		pos, span := &b[a[0]], &b[a[1]]
		*span = math.Min(math.Max(*span, 0), 1)
		*pos = math.Min(math.Max(*pos, 0), 1-*span)
		for *pos+*span > 1 {
			*span = math.Nextafter(*span, 0)
		}
	}
	return b
}

type leaf struct {
	blockID string
	rect    client.Bounds
}

func leaves(n *client.LayoutNode, out []leaf) []leaf {
	if n == nil {
		return out
	}
	if n.Split != nil {
		out = leaves(&n.Split.Before, out)
		return leaves(&n.Split.After, out)
	}
	if n.BlockID != "" {
		out = append(out, leaf{n.BlockID, n.Rect})
	}
	return out
}

// referenceBlock picks the tiled block whose terminal size best stands for
// the window: one filling the whole window when there is one (the case the
// chrome model was measured on), else the largest. A split block's size
// leaves out the split's own chrome, so a window that is only splits is
// scaled up from its largest block and comes out short. Measured on a split
// inside a floating layer (the user's tab is never split for a test): 4
// cells short on either axis at 50/50, 1 to 2 at 70/30 (rex-behaviors.md,
// "Split windows"). Short is the safe side: a smaller window makes
// BoundsForCells ask for a larger fraction, so the layer is bigger than the
// form, never smaller; only the `size` passed for the first frame is a
// little small, and the app resizes the block once it owns it.
func referenceBlock(w client.Window) (leaf, bool) {
	var tiled *client.Layer
	for i := range w.Layers {
		if w.Layers[i].Kind == client.LayerTiled {
			tiled = &w.Layers[i]
			break
		}
	}
	if tiled == nil {
		return leaf{}, false
	}
	var best leaf
	found := false
	for _, l := range leaves(tiled.Layout, nil) {
		if l.rect[2] <= 0 || l.rect[3] <= 0 {
			continue
		}
		if !found || l.rect[2]*l.rect[3] > best.rect[2]*best.rect[3] {
			best, found = l, true
		}
	}
	return best, found
}

// windowCellsFrom scales a tiled block's terminal size up to its window by
// the block's layout rect.
func windowCellsFrom(ts client.TerminalSize, rect client.Bounds) (WindowCells, error) {
	if ts.CellWidthPx <= 0 || ts.CellHeightPx <= 0 {
		return WindowCells{}, ErrNoCellSize
	}
	if ts.Columns <= 0 || ts.Rows <= 0 || rect[2] <= 0 || rect[3] <= 0 {
		return WindowCells{}, fmt.Errorf("rexuikit/surface: unusable reference size %dx%d at %v", ts.Columns, ts.Rows, rect)
	}
	return WindowCells{
		Cols:    int(math.Round(float64(ts.Columns) / rect[2])),
		Rows:    int(math.Round(float64(ts.Rows) / rect[3])),
		CellWPx: ts.CellWidthPx,
		CellHPx: ts.CellHeightPx,
	}, nil
}

// findWindow returns the window named id, or the active one when id is
// empty.
func findWindow(v client.View, id string) (client.Window, bool) {
	if id == "" {
		id = v.ActiveWindowID
	}
	for _, w := range v.Windows {
		if w.WindowID == id {
			return w, true
		}
	}
	return client.Window{}, false
}
