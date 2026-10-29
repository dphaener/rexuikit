package surface

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dphaener/rexuikit/client"
)

type call struct {
	method string
	args   any
}

// fakeRex records every call and answers from scripted state. It never calls
// t.Fatal: some calls arrive on the Surface's event goroutine, and Goexit
// there would hang the test instead of failing it.
type fakeRex struct {
	mu     sync.Mutex
	calls  []call
	view   client.View
	sizes  map[string]client.TerminalSize
	blocks []client.BlockInfo
	events chan client.Event
	fail   map[string]error
	nextID int
	rev    int64
	// viewRev, when set, is the revision View reports instead of the
	// current one: a view read before later changes.
	viewRev int64
}

func newFake() *fakeRex {
	return &fakeRex{
		sizes: map[string]client.TerminalSize{},
		fail:  map[string]error{},
	}
}

// oneWindow scripts a session with one window whose tiled layer is a lone
// block of cols x rows cells at 7x14 px, the shape rex-behaviors.md was
// measured on.
func (f *fakeRex) oneWindow(cols, rows int) *fakeRex {
	f.view = client.View{
		SessionID:      "session:s",
		ActiveWindowID: "window:w",
		Windows: []client.Window{{
			WindowID: "window:w",
			Layers: []client.Layer{{
				LayerID: "layer:tiled", Kind: client.LayerTiled,
				Layout: &client.LayoutNode{Rect: client.Bounds{0, 0, 1, 1}, BlockID: "block:shell"},
			}},
		}},
	}
	f.sizes["block:shell"] = client.TerminalSize{Columns: cols, Rows: rows, CellWidthPx: 7, CellHeightPx: 14}
	return f
}

func (f *fakeRex) record(method string, args any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{method, args})
	f.rev++
	return f.fail[method]
}

func (f *fakeRex) id(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return fmt.Sprintf("%s:%d", prefix, f.nextID)
}

func (f *fakeRex) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.method)
	}
	return out
}

func (f *fakeRex) callsOf(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeRex) curRev() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rev
}

func (f *fakeRex) View(_ context.Context, _ string) (client.View, error) {
	err := f.record("session.view", nil)
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.view
	v.Revision = f.rev
	if f.viewRev != 0 {
		v.Revision = f.viewRev
	}
	return v, err
}

func (f *fakeRex) TerminalSize(_ context.Context, _, block string) (client.TerminalSize, error) {
	err := f.record("com.superlogical.terminal.size", block)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sizes[block], err
}

func (f *fakeRex) NewLayer(_ context.Context, a client.NewLayerArgs) (client.NewLayerResult, error) {
	if err := f.record("session.new_layer", a); err != nil {
		return client.NewLayerResult{}, err
	}
	return client.NewLayerResult{LayerID: f.id("layer"), BlockIDs: []string{f.id("block")}, Revision: f.curRev()}, nil
}

type resizeArgs struct {
	layer  string
	bounds client.Bounds
}

func (f *fakeRex) ResizeLayer(_ context.Context, _, layer string, b client.Bounds) (int64, error) {
	return f.curRev(), f.record("session.resize_layer", resizeArgs{layer, b})
}

func (f *fakeRex) CloseLayer(_ context.Context, _, layer string) (int64, error) {
	return f.curRev(), f.record("session.close_layer", layer)
}

func (f *fakeRex) RaiseLayer(_ context.Context, _, layer, _ string) (int64, error) {
	return f.curRev(), f.record("session.raise_layer", layer)
}

func (f *fakeRex) NewSplit(_ context.Context, a client.NewSplitArgs) (client.NewSplitResult, error) {
	if err := f.record("session.new_split", a); err != nil {
		return client.NewSplitResult{}, err
	}
	return client.NewSplitResult{AnchorID: a.AnchorID, BlockIDs: []string{f.id("block")}, Revision: f.curRev()}, nil
}

func (f *fakeRex) NewBlock(_ context.Context, a client.NewBlockArgs) (string, error) {
	if err := f.record("session.new_block", a); err != nil {
		return "", err
	}
	return f.id("block"), nil
}

func (f *fakeRex) MoveBlock(_ context.Context, a client.MoveBlockArgs) (client.MoveBlockResult, error) {
	return client.MoveBlockResult{Revision: f.curRev()}, f.record("session.move_block", a)
}

func (f *fakeRex) FocusBlock(_ context.Context, _, block string) (int64, error) {
	return f.curRev(), f.record("session.focus_block", block)
}

type labelArgs struct{ block, label string }

func (f *fakeRex) SetBlockLabel(_ context.Context, _, block, label string) (int64, error) {
	return f.curRev(), f.record("session.set_block_label", labelArgs{block, label})
}

func (f *fakeRex) ListBlocks(_ context.Context, _ string) ([]client.BlockInfo, error) {
	err := f.record("session.list_blocks", nil)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blocks, err
}

func (f *fakeRex) CloseBlock(_ context.Context, _, block string) (int64, error) {
	return f.curRev(), f.record("block.close", block)
}

func (f *fakeRex) Call(_ context.Context, method string, payload, _ any) error {
	return f.record(method, payload)
}

func (f *fakeRex) Events() <-chan client.Event {
	if f.events == nil {
		return nil
	}
	return f.events
}

// noSleep records each animation wait instead of sleeping.
func noSleep(waits *[]time.Duration) Option {
	var mu sync.Mutex
	return WithSleep(func(_ context.Context, d time.Duration) error {
		mu.Lock()
		*waits = append(*waits, d)
		mu.Unlock()
		return nil
	})
}

func sessionEvent(t *testing.T, typ string, payload any) client.Event {
	t.Helper()
	inner, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := json.Marshal(map[string]any{
		"session_id": "session:s",
		"event":      map[string]any{"type": typ, "payload": json.RawMessage(inner)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client.Event{Type: "session_event", Payload: outer}
}

func waitDone(t *testing.T, h *Handle) {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not fire")
	}
}

// TestUsable is the forward chrome model against samples recorded in
// rex-behaviors.md (149x130 cells at 7x14 px). Load-bearing rows: the two
// w=0.40 rows at x=0.40 and x=0.55, which differ by a column only because
// each edge rounds on its own; a position-free model passes one and fails
// the other. The tiny-bounds rows pin the 1x1 floor.
func TestUsable(t *testing.T) {
	w149 := WindowCells{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14}
	w80 := WindowCells{Cols: 80, Rows: 24, CellWPx: 7, CellHPx: 14}
	w300 := WindowCells{Cols: 300, Rows: 80, CellWPx: 9, CellHPx: 18}
	tests := []struct {
		name string
		w    WindowCells
		b    client.Bounds
		want client.Size
	}{
		{"doc sample x=0.40", w149, client.Bounds{0.40, 0.30, 0.40, 0.30}, client.Size{Columns: 57, Rows: 36}},
		{"doc sample x=0.55", w149, client.Bounds{0.55, 0.30, 0.40, 0.30}, client.Size{Columns: 58, Rows: 36}},
		{"doc sample 0.8x0.8", w149, client.Bounds{0.1, 0.1, 0.8, 0.8}, client.Size{Columns: 118, Rows: 103}},
		{"doc sample corner", w149, client.Bounds{0, 0, 0.2, 0.2}, client.Size{Columns: 27, Rows: 23}},
		{"doc sample narrow", w149, client.Bounds{0.45, 0.45, 0.1, 0.5}, client.Size{Columns: 12, Rows: 63}},
		// (80*7+21=581 px; edge(0.5)=291, edge(1.0)=581, 290-20=270 -> 38 cols;
		// (24*14+46=382 px; edge(.25)=96, edge(.75)=287, 191-46=145 -> 10 rows)
		{"small window half", w80, client.Bounds{0.5, 0.25, 0.5, 0.5}, client.Size{Columns: 38, Rows: 10}},
		// (300*9+21=2721 px; edge(.2)=544, edge(.8)=2177, 1633-20=1613 -> 179;
		// (80*18+46=1486 px; edge(.1)=149, edge(.9)=1337, 1188-46=1142 -> 63)
		{"other cell size", w300, client.Bounds{0.2, 0.1, 0.6, 0.8}, client.Size{Columns: 179, Rows: 63}},
		{"never below 1x1", w80, client.Bounds{0.5, 0.5, 0.001, 0.001}, client.Size{Columns: 1, Rows: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Usable(tt.w, tt.b); got != tt.want {
				t.Errorf("Usable(%v) = %dx%d, want %dx%d", tt.b, got.Columns, got.Rows, tt.want.Columns, tt.want.Rows)
			}
		})
	}
}

// TestBoundsForCellsRoundTrip: the inverse lands within one cell of the
// asked size (never short), sits where the anchor says, and is the smallest
// such span to a pixel. Load-bearing: the centred rows on 149 columns, where
// re-placing x while growing is what keeps the result from coming up a
// column short; the too-big row pins the clamp.
func TestBoundsForCellsRoundTrip(t *testing.T) {
	windows := []WindowCells{
		{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14},
		{Cols: 80, Rows: 24, CellWPx: 7, CellHPx: 14},
		{Cols: 213, Rows: 57, CellWPx: 8, CellHPx: 17},
	}
	anchors := map[string]Anchor{
		"centered": Centered, "right": RightEdge, "left": LeftEdge, "top": TopEdge, "bottom": BottomEdge,
	}
	sizes := [][2]int{{1, 1}, {10, 3}, {46, 13}, {46, 30}, {57, 36}, {58, 20}, {76, 7}}
	for _, w := range windows {
		for an, a := range anchors {
			for _, sz := range sizes {
				name := fmt.Sprintf("%dx%d/%s/%dx%d", w.Cols, w.Rows, an, sz[0], sz[1])
				t.Run(name, func(t *testing.T) {
					b, ok := BoundsForCells(w, sz[0], sz[1], a)
					if b[0] < 0 || b[1] < 0 || b[0]+b[2] > 1 || b[1]+b[3] > 1 {
						t.Fatalf("bounds %v leave the window", b)
					}
					got := Usable(w, b)
					if !ok {
						if got.Columns >= sz[0] && got.Rows >= sz[1] {
							t.Fatalf("ok=false but %v gives %dx%d", b, got.Columns, got.Rows)
						}
						return
					}
					if got.Columns < sz[0] || got.Columns > sz[0]+1 || got.Rows < sz[1] || got.Rows > sz[1]+1 {
						t.Errorf("round trip %v -> %dx%d, want %dx%d (+1 at most)", b, got.Columns, got.Rows, sz[0], sz[1])
					}
					// One pixel narrower at the same anchor must fall short,
					// or the span was not the smallest.
					px := axisPx(w.Cols, FramePxCols, w.CellWPx)
					if sw := b[2] - 1.5/px; sw > 0 {
						if usableAxis(w.Cols, FramePxCols, w.CellWPx, BorderPxCols, placeAxis(a.H, sw), sw) >= sz[0]+1 {
							t.Errorf("a span 1.5px narrower still gives %d+1 cols: not minimal", sz[0])
						}
					}
					checkAnchor(t, a.H, b[0], b[2])
					checkAnchor(t, a.V, b[1], b[3])
				})
			}
		}
	}
	t.Run("too big clamps to the window", func(t *testing.T) {
		w := windows[1]
		b, ok := BoundsForCells(w, 500, 10, Centered)
		if ok {
			t.Fatal("ok = true for 500 cols in an 80-col window")
		}
		if b[0] != 0 || b[2] != 1 {
			t.Errorf("cols axis %v, want x=0 w=1", b)
		}
		if got := Usable(w, b); got.Rows < 10 {
			t.Errorf("rows axis still sized: got %d rows, want >= 10", got.Rows)
		}
	})
}

// TestBoundsForCellsNeverShort: forward(inverse(n)) >= n on both axes for
// every anchor, over windows and cell sizes beyond the one the chrome was
// measured on, with the inverse margins spare in pixels; and ok is false
// only when Usable really falls short. Load-bearing: the 149x130 @ 7x14
// window with a 46x30 form, the new-session layer W6.T2 saw settle at
// 46x29 when its rows spanned exactly 30*14 + 44 px under the old model;
// the re-measured model (1866 px, border 46) reproduces that 29.
func TestBoundsForCellsNeverShort(t *testing.T) {
	windows := []WindowCells{
		{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14},
		{Cols: 148, Rows: 130, CellWPx: 7, CellHPx: 14},
		{Cols: 80, Rows: 24, CellWPx: 7, CellHPx: 14},
		{Cols: 213, Rows: 57, CellWPx: 8, CellHPx: 17},
		{Cols: 97, Rows: 41, CellWPx: 9, CellHPx: 19},
		{Cols: 255, Rows: 70, CellWPx: 7, CellHPx: 15},
		{Cols: 120, Rows: 40, CellWPx: 10, CellHPx: 22},
		{Cols: 60, Rows: 20, CellWPx: 6, CellHPx: 13},
	}
	anchors := []Anchor{Centered, LeftEdge, RightEdge, TopEdge, BottomEdge, {H: Start, V: End}}
	spare := func(cells, framePx, cellPx, borderPx, want int, pos, span float64) float64 {
		p := axisPx(cells, framePx, cellPx)
		return roundEdge(p*(pos+span)) - roundEdge(p*pos) - float64(borderPx+want*cellPx)
	}
	for _, w := range windows {
		for _, a := range anchors {
			for cols := 1; cols <= w.Cols+2; cols += 3 {
				for rows := 1; rows <= w.Rows+2; rows++ {
					b, ok := BoundsForCells(w, cols, rows, a)
					got := Usable(w, b)
					short := got.Columns < cols || got.Rows < rows
					if ok == short {
						t.Fatalf("%dx%d@%dx%d %+v: BoundsForCells(%d, %d) = %v ok=%v, Usable %dx%d",
							w.Cols, w.Rows, w.CellWPx, w.CellHPx, a, cols, rows, b, ok, got.Columns, got.Rows)
					}
					if !ok {
						continue
					}
					if b[2] < 1 {
						if sp := spare(w.Cols, FramePxCols, w.CellWPx, BorderPxCols, cols, b[0], b[2]); sp < InverseMarginPxCols {
							t.Fatalf("%v cols %d keep %v px spare, want >= %d", b, cols, sp, InverseMarginPxCols)
						}
					}
					if b[3] < 1 {
						if sp := spare(w.Rows, FramePxRows, w.CellHPx, BorderPxRows, rows, b[1], b[3]); sp < InverseMarginPxRows {
							t.Fatalf("%v rows %d keep %v px spare, want >= %d", b, rows, sp, InverseMarginPxRows)
						}
					}
				}
			}
		}
	}
	t.Run("W6.T2 new-session layer", func(t *testing.T) {
		w := WindowCells{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14}
		b, ok := BoundsForCells(w, 46, 30, Centered)
		if !ok {
			t.Fatal("ok = false for a 46x30 form in a 149x130 window")
		}
		if got := Usable(w, b); got.Columns < 46 || got.Rows < 30 {
			t.Errorf("Usable = %dx%d, want >= 46x30", got.Columns, got.Rows)
		}
		// The bounds W6.T2 opened (no margin, old model) must be what the
		// re-measured model calls 29 rows: the app gave 29 every time.
		old := client.Bounds{0.3392857142857143, 0.3754027926960258, 0.32142857142857145, 0.2491944146079484}
		if got := Usable(w, old); got.Columns != 46 || got.Rows != 29 {
			t.Errorf("Usable(W6.T2 bounds) = %dx%d, want the 46x29 the app gave", got.Columns, got.Rows)
		}
		// 5 px, not InverseMarginPxRows: the constant must not be able to
		// shrink below the spare the inverse is documented to keep.
		const minSpare = 5
		p := axisPx(w.Rows, FramePxRows, w.CellHPx)
		top, bottom := roundEdge(p*b[1]), roundEdge(p*(b[1]+b[3]))
		if bottom-top < float64(30*14+BorderPxRows+minSpare) {
			t.Errorf("rows span %v..%v = %v px, want >= %d", top, bottom, bottom-top, 30*14+BorderPxRows+minSpare)
		}
	})
}

func checkAnchor(t *testing.T, p Place, pos, span float64) {
	t.Helper()
	const eps = 1e-9
	switch p {
	case Start:
		if pos != 0 {
			t.Errorf("start anchor pos = %v, want 0", pos)
		}
	case End:
		if math.Abs(pos+span-1) > eps {
			t.Errorf("end anchor ends at %v, want 1", pos+span)
		}
	default:
		if math.Abs(pos-(1-span)/2) > eps {
			t.Errorf("centred pos = %v, want %v", pos, (1-span)/2)
		}
	}
}

// TestWindowCells covers reading a window's size from the view. Load-bearing:
// "split picks largest", which scales a half-width block up rather than
// taking its own size for the window's, and "no cell px", which must fail
// rather than guess.
func TestWindowCells(t *testing.T) {
	split := client.LayoutNode{Split: &client.ViewSplit{
		Before: client.LayoutNode{Rect: client.Bounds{0, 0, 0.25, 1}, BlockID: "block:a"},
		After:  client.LayoutNode{Rect: client.Bounds{0.25, 0, 0.75, 1}, BlockID: "block:b"},
	}}
	tests := []struct {
		name    string
		layout  *client.LayoutNode
		sizes   map[string]client.TerminalSize
		window  string
		want    WindowCells
		wantErr bool
	}{
		{
			name:   "lone block",
			layout: &client.LayoutNode{Rect: client.Bounds{0, 0, 1, 1}, BlockID: "block:a"},
			sizes:  map[string]client.TerminalSize{"block:a": {Columns: 149, Rows: 130, CellWidthPx: 7, CellHeightPx: 14}},
			want:   WindowCells{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14},
		},
		{
			name:   "split picks largest",
			layout: &split,
			sizes:  map[string]client.TerminalSize{"block:b": {Columns: 111, Rows: 40, CellWidthPx: 7, CellHeightPx: 14}},
			want:   WindowCells{Cols: 148, Rows: 40, CellWPx: 7, CellHPx: 14},
		},
		{
			name:    "no cell px",
			layout:  &client.LayoutNode{Rect: client.Bounds{0, 0, 1, 1}, BlockID: "block:a"},
			sizes:   map[string]client.TerminalSize{"block:a": {Columns: 80, Rows: 24}},
			wantErr: true,
		},
		{name: "no tiled block", layout: nil, wantErr: true},
		{
			name:    "unknown window",
			layout:  &client.LayoutNode{Rect: client.Bounds{0, 0, 1, 1}, BlockID: "block:a"},
			window:  "window:nope",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			f.view = client.View{ActiveWindowID: "window:w", Windows: []client.Window{{
				WindowID: "window:w",
				Layers:   []client.Layer{{LayerID: "layer:t", Kind: client.LayerTiled, Layout: tt.layout}},
			}}}
			if tt.sizes != nil {
				f.sizes = tt.sizes
			}
			got, wid, err := New(f, "session:s").Window(context.Background(), tt.window)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Window = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || wid != "window:w" {
				t.Errorf("Window = %+v in %q, want %+v in window:w", got, wid, tt.want)
			}
		})
	}
}

func layerArgs(t *testing.T, c call) client.NewLayerArgs {
	t.Helper()
	a, ok := c.args.(client.NewLayerArgs)
	if !ok {
		t.Fatalf("new_layer args %T", c.args)
	}
	return a
}

func termOpts(t *testing.T, l client.Layout) client.TerminalOptions {
	t.Helper()
	if l.Block == nil {
		t.Fatal("layout has no block")
	}
	o, ok := l.Block.Options.(client.TerminalOptions)
	if !ok {
		t.Fatalf("block options %T", l.Block.Options)
	}
	return o
}

// TestOpenModalSized: a modal sized from Cells passes the computed `size`,
// so the program's first frame is right, and is disowned so the app sizes
// it after.
func TestOpenModalSized(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	s := New(f, "session:s")
	h, err := s.Open(context.Background(), Spec{
		Kind: Modal, Argv: []string{"forge", "quick", "new"}, Cwd: "/w", Label: "New session",
		Cells: client.Size{Columns: 46, Rows: 30}, Focus: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"session.view", "com.superlogical.terminal.size", "session.new_layer", "session.disown_block"}
	if got := f.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	a := layerArgs(t, f.callsOf("session.new_layer")[0])
	o := termOpts(t, a.Layout)
	wc := WindowCells{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14}
	if o.Size == nil || *o.Size != Usable(wc, a.Bounds) {
		t.Fatalf("size = %v, want Usable(bounds) = %v", o.Size, Usable(wc, a.Bounds))
	}
	if o.Size.Columns < 46 || o.Size.Rows < 30 {
		t.Errorf("size %dx%d smaller than the content 46x30", o.Size.Columns, o.Size.Rows)
	}
	if o.Shell != "none" || strings.Join(o.Command, " ") != "forge quick new" || o.Cwd != "/w" {
		t.Errorf("options = %+v", o)
	}
	if !a.Focus || a.WindowID != "window:w" || a.Layout.Block.Label != "New session" {
		t.Errorf("new_layer args = %+v", a)
	}
	checkAnchor(t, Center, a.Bounds[0], a.Bounds[2])
	if h.LayerID() == "" || h.BlockID() == "" || h.Kind() != Modal {
		t.Errorf("handle = %+v", h)
	}
}

// TestDrawerOpen: one new_layer at a sliver, then N resize_layer calls whose
// spans only grow, ending exactly at the target bounds, and the far edge
// stays put on every frame. Disown comes after the animation, never before.
func TestDrawerOpen(t *testing.T) {
	for _, edge := range []Edge{EdgeRight, EdgeLeft, EdgeTop, EdgeBottom} {
		t.Run(fmt.Sprint(edge), func(t *testing.T) {
			f := newFake().oneWindow(149, 130)
			var waits []time.Duration
			s := New(f, "session:s", WithAnimation(12, 16*time.Millisecond), noSleep(&waits))
			target := client.Bounds{0.6, 0.1, 0.4, 0.8}
			if edge == EdgeLeft {
				target = client.Bounds{0, 0.1, 0.4, 0.8}
			}
			if edge == EdgeTop || edge == EdgeBottom {
				target = client.Bounds{0.1, 0, 0.8, 0.4}
				if edge == EdgeBottom {
					target[1] = 0.6
				}
			}
			h, err := s.Open(context.Background(), Spec{Kind: Drawer, Edge: edge, Argv: []string{"x"}, Bounds: target})
			if err != nil {
				t.Fatal(err)
			}
			m := f.methods()
			if m[2] != "session.new_layer" || m[len(m)-1] != "session.disown_block" {
				t.Fatalf("calls = %v", m)
			}
			resizes := f.callsOf("session.resize_layer")
			if len(resizes) != 12 || len(waits) != 12 {
				t.Fatalf("%d resizes, %d waits, want 12 each", len(resizes), len(waits))
			}
			axis := 2
			if edge == EdgeTop || edge == EdgeBottom {
				axis = 3
			}
			prev := layerArgs(t, f.callsOf("session.new_layer")[0]).Bounds
			if prev[axis] >= target[axis]/4 {
				t.Errorf("first frame %v is not a sliver", prev)
			}
			far := func(b client.Bounds) float64 {
				switch edge {
				case EdgeLeft:
					return b[0]
				case EdgeTop:
					return b[1]
				case EdgeBottom:
					return b[1] + b[3]
				}
				return b[0] + b[2]
			}
			for i, c := range resizes {
				r := c.args.(resizeArgs)
				if r.layer != h.LayerID() {
					t.Errorf("resize %d targets %s", i, r.layer)
				}
				if r.bounds[axis] <= prev[axis] {
					t.Errorf("resize %d span %v not above %v", i, r.bounds[axis], prev[axis])
				}
				if math.Abs(far(r.bounds)-far(target)) > 1e-9 {
					t.Errorf("resize %d moved the anchored edge: %v", i, r.bounds)
				}
				prev = r.bounds
			}
			if prev != target {
				t.Errorf("last frame %v, want target %v", prev, target)
			}
			// The program was sized for the final bounds, not the sliver.
			o := termOpts(t, layerArgs(t, f.callsOf("session.new_layer")[0]).Layout)
			if want := Usable(WindowCells{149, 130, 7, 14}, target); *o.Size != want {
				t.Errorf("size %v, want final %v", *o.Size, want)
			}
		})
	}
}

// TestDrawerClose slides back out through the same frames, then closes.
func TestDrawerClose(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	var waits []time.Duration
	s := New(f, "session:s", WithAnimation(4, time.Millisecond), noSleep(&waits))
	h, err := s.Open(context.Background(), Spec{Kind: Drawer, Argv: []string{"x"}, Bounds: client.Bounds{0.5, 0, 0.5, 1}})
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.callsOf("session.resize_layer"))
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := f.callsOf("session.resize_layer")[before:]
	if len(all) != 4 {
		t.Fatalf("%d close frames, want 4", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].args.(resizeArgs).bounds[2] >= all[i-1].args.(resizeArgs).bounds[2] {
			t.Errorf("close frame %d did not shrink", i)
		}
	}
	m := f.methods()
	if m[len(m)-1] != "session.close_layer" {
		t.Errorf("last call %s, want session.close_layer", m[len(m)-1])
	}
	waitDone(t, h)
}

// TestOpenSplit: a split beside the anchor block, defaults filled in, no
// size (splits are sized by the app), disowned.
func TestOpenSplit(t *testing.T) {
	f := newFake()
	h, err := New(f, "session:s").Open(context.Background(), Spec{
		Kind: Split, Argv: []string{"forge", "quick", "info"}, AnchorBlock: "block:anchor", Focus: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.methods(), ","); got != "session.new_split,session.disown_block" {
		t.Fatalf("calls = %s", got)
	}
	a := f.callsOf("session.new_split")[0].args.(client.NewSplitArgs)
	if a.AnchorID != "block:anchor" || a.Direction != client.Horizontal || a.Side != client.After || !a.Focus {
		t.Errorf("new_split args = %+v", a)
	}
	if termOpts(t, a.Layout).Size != nil {
		t.Error("split passed a size")
	}
	if h.LayerID() != "" {
		t.Error("split handle has a layer")
	}
	if err := h.Resize(context.Background(), client.Bounds{0, 0, 1, 1}); err != ErrNotLayer {
		t.Errorf("Resize on a split = %v, want ErrNotLayer", err)
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := f.callsOf("block.close"); len(c) != 1 || c[0].args != h.BlockID() {
		t.Errorf("close = %v, want block.close of %s", c, h.BlockID())
	}
}

// TestOpenErrors: refusals before anything is created, and cleanup when a
// later step fails. Load-bearing: "disown fails", which must close the layer
// it just opened rather than leave an orphan the app can never size.
func TestOpenErrors(t *testing.T) {
	tests := []struct {
		name      string
		spec      Spec
		fail      string
		wantClose bool
	}{
		{name: "no argv", spec: Spec{Kind: Modal, Bounds: client.Bounds{0, 0, 1, 1}}},
		{name: "no bounds or cells", spec: Spec{Kind: Modal, Argv: []string{"x"}}},
		{name: "split without anchor", spec: Spec{Kind: Split, Argv: []string{"x"}}},
		{name: "new_layer fails", spec: Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0, 0, 1, 1}}, fail: "session.new_layer"},
		{name: "disown fails", spec: Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0, 0, 1, 1}}, fail: "session.disown_block", wantClose: true},
		{name: "drawer resize fails", spec: Spec{Kind: Drawer, Argv: []string{"x"}, Bounds: client.Bounds{0.5, 0, 0.5, 1}}, fail: "session.resize_layer", wantClose: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake().oneWindow(149, 130)
			if tt.fail != "" {
				f.fail[tt.fail] = &client.Error{Code: "conflict", Status: 409}
			}
			var waits []time.Duration
			_, err := New(f, "session:s", noSleep(&waits)).Open(context.Background(), tt.spec)
			if err == nil {
				t.Fatal("Open succeeded")
			}
			if got := len(f.callsOf("session.close_layer")) == 1; got != tt.wantClose {
				t.Errorf("closed layer = %v, want %v (calls %v)", got, tt.wantClose, f.methods())
			}
		})
	}
}

// TestHandleResizeRaise passes through to the layer and pulls bounds back
// inside the window.
func TestHandleResizeRaise(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	h, err := New(f, "session:s").Open(context.Background(), Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0.2, 0.2, 0.5, 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Resize(context.Background(), client.Bounds{0.8, 0.1, 0.5, 0.5}); err != nil {
		t.Fatal(err)
	}
	r := f.callsOf("session.resize_layer")[0].args.(resizeArgs)
	if r.bounds[0]+r.bounds[2] > 1 || r.bounds[2] != 0.5 {
		t.Errorf("resize sent %v, want pulled inside with w kept", r.bounds)
	}
	if h.Bounds() != r.bounds {
		t.Errorf("Bounds() = %v, want %v", h.Bounds(), r.bounds)
	}
	if err := h.Raise(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := f.callsOf("session.raise_layer"); len(c) != 1 || c[0].args != h.LayerID() {
		t.Errorf("raise = %v", c)
	}
}

// TestDoneOnBlockClosed: Done fires for the handle's own block_closed and
// not for another block's, or for another session's.
func TestDoneOnBlockClosed(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	f.events = make(chan client.Event, 8)
	defer close(f.events)
	s := New(f, "session:s")
	h, err := s.Open(context.Background(), Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0, 0, 0.5, 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	f.events <- sessionEvent(t, "block_closed", map[string]string{"block_id": "block:other"})
	other := sessionEvent(t, "block_closed", map[string]string{"block_id": h.BlockID()})
	var w map[string]any
	_ = json.Unmarshal(other.Payload, &w)
	w["session_id"] = "session:elsewhere"
	other.Payload, _ = json.Marshal(w)
	f.events <- other
	select {
	case <-h.Done():
		t.Fatal("Done fired for another block or session")
	case <-time.After(50 * time.Millisecond):
	}
	f.events <- sessionEvent(t, "block_closed", map[string]string{"block_id": h.BlockID()})
	waitDone(t, h)
	// Closing a surface that is already gone is not an error and sends
	// nothing.
	n := len(f.methods())
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.methods()) != n {
		t.Errorf("Close after Done sent %v", f.methods()[n:])
	}
}

// TestDoneOnLayerRemoved: a layer removed without its block closing (moved
// out, for example) ends the handle once a newer view lacks it; a view
// older than the handle never does.
func TestDoneOnLayerRemoved(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	f.events = make(chan client.Event, 8)
	defer close(f.events)
	s := New(f, "session:s")
	h, err := s.Open(context.Background(), Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0, 0, 0.5, 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	// The fake's view never lists the layer. A view read at revision 1,
	// before the layer existed, must not end it.
	f.mu.Lock()
	f.viewRev = 1
	f.mu.Unlock()
	f.events <- sessionEvent(t, "session_view_changed", map[string]int64{"revision": 5})
	select {
	case <-h.Done():
		t.Fatal("Done fired from a view older than the layer")
	case <-time.After(50 * time.Millisecond):
	}
	f.mu.Lock()
	f.viewRev = 0
	f.mu.Unlock()
	f.events <- sessionEvent(t, "session_view_changed", map[string]int64{"revision": f.curRev() + 100})
	waitDone(t, h)
}

// TestDoneOnConnectionEnd: once the event stream ends nothing can be
// observed, so open handles finish rather than wait forever.
func TestDoneOnConnectionEnd(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	f.events = make(chan client.Event)
	s := New(f, "session:s")
	h, err := s.Open(context.Background(), Spec{Kind: Modal, Argv: []string{"x"}, Bounds: client.Bounds{0, 0, 0.5, 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	close(f.events)
	waitDone(t, h)
}

// TestWarm: a detached block at the computed size with the warm label,
// disowned at once.
func TestWarm(t *testing.T) {
	f := newFake().oneWindow(149, 130)
	wb, err := New(f, "session:s").Warm(context.Background(), Spec{
		Argv: []string{"forge", "quick", "new", "--prewarmed"}, Label: WarmLabel("new", "b1"),
		Cells: client.Size{Columns: 46, Rows: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.methods(), ","); got != "session.view,com.superlogical.terminal.size,session.new_block,session.disown_block" {
		t.Fatalf("calls = %s", got)
	}
	a := f.callsOf("session.new_block")[0].args.(client.NewBlockArgs)
	o := a.Options.(client.TerminalOptions)
	if a.Label != "forge-warm:new:b1" || a.Flavor != client.TerminalFlavor || o.Size == nil || *o.Size != wb.Size {
		t.Errorf("new_block = %+v (opts %+v), warm %+v", a, o, wb)
	}
	if wb.Size.Columns < 46 || wb.Size.Rows < 30 {
		t.Errorf("warm size %v below the content", wb.Size)
	}
}

// TestPlace: exactly new_layer -> move_block -> block.close in that order,
// the warm block moved beside the placeholder and the placeholder closed,
// then the warm block relabelled to the form's title (what Rex's title bar
// shows), focused and disowned; the handle targets the warm block, not the
// placeholder. The empty and warm-prefixed title rows are load-bearing: a
// placed block must never keep or regain a label FindWarm could match.
func TestPlace(t *testing.T) {
	f := newFake()
	s := New(f, "session:s")
	wb := WarmBlock{BlockID: "block:warm", Label: WarmLabel("new", "b1"), WindowID: "window:w"}
	bounds := client.Bounds{0.3, 0.2, 0.4, 0.5}
	h, err := s.Place(context.Background(), wb, bounds, "New session", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"session.new_layer", "session.move_block", "block.close", "session.set_block_label", "session.focus_block", "session.disown_block"}
	if got := f.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	nl := layerArgs(t, f.callsOf("session.new_layer")[0])
	if nl.Bounds != bounds || !nl.Focus || nl.WindowID != "window:w" {
		t.Errorf("new_layer = %+v", nl)
	}
	if nl.Layout.Block == nil || nl.Layout.Block.Label != "New session" {
		t.Errorf("placeholder layout = %+v, want the title as its label (the layer's title bar while it is alone)", nl.Layout.Block)
	}
	placeholder := "block:2" // the fake's ids: layer:1, block:2
	mv := f.callsOf("session.move_block")[0].args.(client.MoveBlockArgs)
	if mv.BlockID != "block:warm" || mv.AnchorBlockID != placeholder {
		t.Errorf("move_block = %+v, want warm beside %s", mv, placeholder)
	}
	if c := f.callsOf("block.close")[0].args; c != placeholder {
		t.Errorf("block.close %v, want the placeholder", c)
	}
	if l := f.callsOf("session.set_block_label")[0].args.(labelArgs); l != (labelArgs{"block:warm", "New session"}) {
		t.Errorf("relabel = %+v", l)
	}
	if c := f.callsOf("session.focus_block")[0].args; c != "block:warm" {
		t.Errorf("focus %v", c)
	}
	if h.BlockID() != "block:warm" || h.LayerID() != "layer:1" {
		t.Errorf("handle targets %s in %s", h.BlockID(), h.LayerID())
	}

	t.Run("no focus", func(t *testing.T) {
		f := newFake()
		if _, err := New(f, "session:s").Place(context.Background(), wb, bounds, "New session", false); err != nil {
			t.Fatal(err)
		}
		if len(f.callsOf("session.focus_block")) != 0 {
			t.Error("focused without being asked")
		}
	})
	t.Run("move fails closes the placeholder layer", func(t *testing.T) {
		f := newFake()
		f.fail["session.move_block"] = &client.Error{Code: "not_found", Status: 404}
		if _, err := New(f, "session:s").Place(context.Background(), wb, bounds, "New session", true); err == nil {
			t.Fatal("Place succeeded")
		}
		if len(f.callsOf("session.close_layer")) != 1 {
			t.Errorf("calls %v, want the layer closed", f.methods())
		}
	})
	for _, title := range []string{"", "forge-warm:new:b1"} {
		t.Run(fmt.Sprintf("title %q refused before placing", title), func(t *testing.T) {
			f := newFake()
			if _, err := New(f, "session:s").Place(context.Background(), wb, bounds, title, true); err == nil {
				t.Fatal("Place succeeded")
			}
			if len(f.methods()) != 0 {
				t.Errorf("calls %v, want none", f.methods())
			}
		})
	}
}

// TestFindWarm: prefix AND detached, oldest (smallest uuidv7 id) first.
// Load-bearing: the placed block with a warm label (the window between
// placement and relabel) and the action that is a prefix of another
// ("new" vs "newer").
func TestFindWarm(t *testing.T) {
	blocks := []client.BlockInfo{
		{BlockID: "block:0190-a", Label: "forge-warm:new:b1", Placement: client.Placed},
		{BlockID: "block:0193-c", Label: "forge-warm:new:b1", Placement: client.Detached},
		{BlockID: "block:0192-b", Label: "forge-warm:new:b2", Placement: client.Detached},
		{BlockID: "block:0191-x", Label: "forge-warm:newer:b1", Placement: client.Detached},
		{BlockID: "block:0189-y", Label: "forge-warm:oneoff:b1", Placement: client.Detached},
		{BlockID: "block:0188-z", Label: "New session", Placement: client.Detached},
	}
	tests := []struct {
		action string
		want   string
	}{
		{"new", "block:0192-b"},
		{"oneoff", "block:0189-y"},
		{"newer", "block:0191-x"},
		{"label", ""},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			got, ok := FindWarm(blocks, tt.action)
			if ok != (tt.want != "") || got.BlockID != tt.want {
				t.Errorf("FindWarm(%s) = %q, %v; want %q", tt.action, got.BlockID, ok, tt.want)
			}
		})
	}
}

func TestLabels(t *testing.T) {
	if got := WarmLabel("new", "abc"); got != "forge-warm:new:abc" {
		t.Errorf("WarmLabel = %q", got)
	}
}

// TestContextPath: sanitizing is byte-wise (a multi-byte rune becomes one
// '_' per byte, which is what Lua's byte-oriented gsub produces) and the
// path is under the HOME-derived dir.
func TestContextPath(t *testing.T) {
	dir := PrewarmDir("/Users/me")
	if dir != "/Users/me/.local/state/forge/prewarm" {
		t.Fatalf("PrewarmDir = %q", dir)
	}
	tests := []struct{ session, want string }{
		{"session:0190f3a2-7c1e-7b2a-9d4f-0123456789ab", "session_0190f3a2-7c1e-7b2a-9d4f-0123456789ab-new.json"},
		{"a/b\\c d.e", "a_b_c_d_e-new.json"},
		{"é", "__-new.json"},
		{"ok_Z-9", "ok_Z-9-new.json"},
	}
	for _, tt := range tests {
		if got := ContextPath(dir, tt.session, "new"); got != filepath.Join(dir, tt.want) {
			t.Errorf("ContextPath(%q) = %q, want %q", tt.session, got, tt.want)
		}
	}
}

// TestContextRoundTrip writes and reads a context file, overwriting an
// existing one without leaving temp files behind.
func TestContextRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "prewarm")
	path := ContextPath(dir, "session:s", "new")
	pc := PlacementContext{OpenerBlockID: "block:o", Cwd: "/w", SessionID: "session:s", WindowID: "window:w", Timestamp: 1798000000}
	for i := 0; i < 2; i++ {
		pc.Timestamp += int64(i)
		if err := WriteContext(path, pc); err != nil {
			t.Fatal(err)
		}
		got, err := ReadContext(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != pc {
			t.Errorf("round trip = %+v, want %+v", got, pc)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir holds %d entries, want only the context file", len(entries))
	}
	raw, _ := os.ReadFile(path)
	for _, k := range []string{"opener_block_id", "cwd", "session_id", "window_id", "timestamp"} {
		if !strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("file lacks key %s: %s", k, raw)
		}
	}
	if _, err := ReadContext(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("ReadContext of a missing file succeeded")
	}
}

var chromeRow = regexp.MustCompile("(?m)^\\|\\s*`?(FramePxCols|FramePxRows|BorderPxCols|BorderPxRows|EdgeRounding|SizeRounding|InverseMarginPxCols|InverseMarginPxRows)`?\\s*\\|\\s*(\\w+)\\s*\\|")

var sampleRow = regexp.MustCompile(`(?m)^\|\s*([\d.]+),\s*([\d.]+),\s*([\d.]+),\s*([\d.]+)\s*\|\s*(\d+)\s*\|\s*(\d+)\s*\|`)

func chromeSection(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "docs", "rex-behaviors.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i := strings.Index(doc, "\n## Layer chrome")
	if i < 0 {
		t.Fatal("rex-behaviors.md has no `## Layer chrome` section")
	}
	sec := doc[i+1:]
	if j := strings.Index(sec[1:], "\n## "); j >= 0 {
		sec = sec[:j+1]
	}
	return sec
}

// TestChromeConstantsMatchBehaviorsDoc: the exported constants equal the
// tables in rex-behaviors.md `## Layer chrome` (the chrome, and the inverse
// margins under `### Inverse`), the single source the Lua is generated
// from too, so Go, Lua and the doc cannot drift apart silently.
func TestChromeConstantsMatchBehaviorsDoc(t *testing.T) {
	v := map[string]string{}
	for _, m := range chromeRow.FindAllStringSubmatch(chromeSection(t), -1) {
		if _, dup := v[m[1]]; !dup {
			v[m[1]] = m[2]
		}
	}
	ints := map[string]int{
		"FramePxCols": FramePxCols, "FramePxRows": FramePxRows,
		"BorderPxCols": BorderPxCols, "BorderPxRows": BorderPxRows,
		"InverseMarginPxCols": InverseMarginPxCols, "InverseMarginPxRows": InverseMarginPxRows,
	}
	for k, want := range ints {
		got, err := strconv.Atoi(v[k])
		if err != nil {
			t.Errorf("doc %s = %q, not an integer", k, v[k])
			continue
		}
		if got != want {
			t.Errorf("%s = %d, doc says %d", k, want, got)
		}
	}
	if v["EdgeRounding"] != EdgeRounding || v["SizeRounding"] != SizeRounding {
		t.Errorf("roundings %s/%s, doc says %s/%s", EdgeRounding, SizeRounding, v["EdgeRounding"], v["SizeRounding"])
	}
}

// TestUsableMatchesRecordedSamples replays every measured sample in the
// doc's evidence table (149x130 cells, 7x14 px) through Usable, including
// the centred 46x30 layer (0.339286, 0.375403, ...) the app gave 29 rows.
func TestUsableMatchesRecordedSamples(t *testing.T) {
	w := WindowCells{Cols: 149, Rows: 130, CellWPx: 7, CellHPx: 14}
	rows := sampleRow.FindAllStringSubmatch(chromeSection(t), -1)
	if len(rows) < 96 {
		t.Fatalf("found %d samples in the doc, want the 96 recorded", len(rows))
	}
	for _, m := range rows {
		var b client.Bounds
		for i := range b {
			b[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		cols, _ := strconv.Atoi(m[5])
		rws, _ := strconv.Atoi(m[6])
		if got := Usable(w, b); got.Columns != cols || got.Rows != rws {
			t.Errorf("Usable(%v) = %dx%d, measured %dx%d", b, got.Columns, got.Rows, cols, rws)
		}
	}
}

// TestFitKeepsBoundsInside guards the float edge the server rejects.
func TestFitKeepsBoundsInside(t *testing.T) {
	for _, b := range []client.Bounds{
		{0.7, 0.7, 0.3000000000000001, 0.3000000000000001},
		{-0.1, 1.2, 0.5, 0.5},
		{0.9, 0.9, 2, 2},
	} {
		got := fit(b)
		if got[0] < 0 || got[1] < 0 || got[0]+got[2] > 1 || got[1]+got[3] > 1 {
			t.Errorf("fit(%v) = %v leaves the window", b, got)
		}
	}
}
