// Package surface opens, sizes, animates, focuses and closes the places a
// program can appear in Rex: centred modal layers, edge-anchored drawers
// that slide in, and splits beside a block. It also keeps a pool of
// pre-warmed detached blocks that can be placed with their content already
// rendered.
//
// Everything goes through Rex's control connection (rexuikit/client), never
// the rex CLI: a CLI call costs ~76ms, a call on an open connection 0.1-3ms
// (docs/rex-behaviors.md, measured latencies). The package depends on the
// narrow Rex interface below rather than *client.Client so its decisions are
// table-testable against a fake.
//
// Sizing is the reason this package exists. A layer opened without `size`
// starts at 80x24 and its first frame is drawn at that size, so every open
// here computes the usable cell size from the window and the chrome model in
// rex-behaviors.md `## Layer chrome` and passes it as `size`.
//
// Ownership: the connection that creates a block owns it, and while a
// non-app client owns a block the Rex app's size requests are not applied
// (rex-behaviors.md; `session.disown_block`). Every block opened here is
// disowned once it is in place, so the app sizes it from then on. Disowning
// needs a connection attached with session.attach, not session.attach_all.
package surface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/dphaener/rexuikit/client"
)

// Rex is the part of *client.Client this package uses. Events may return
// nil (a client dialled with DisableEvents); Done then fires only on Close.
type Rex interface {
	View(ctx context.Context, sessionID string) (client.View, error)
	TerminalSize(ctx context.Context, sessionID, blockID string) (client.TerminalSize, error)
	NewLayer(ctx context.Context, a client.NewLayerArgs) (client.NewLayerResult, error)
	ResizeLayer(ctx context.Context, sessionID, layerID string, b client.Bounds) (int64, error)
	CloseLayer(ctx context.Context, sessionID, layerID string) (int64, error)
	RaiseLayer(ctx context.Context, sessionID, layerID, relativeLayerID string) (int64, error)
	NewSplit(ctx context.Context, a client.NewSplitArgs) (client.NewSplitResult, error)
	NewBlock(ctx context.Context, a client.NewBlockArgs) (string, error)
	MoveBlock(ctx context.Context, a client.MoveBlockArgs) (client.MoveBlockResult, error)
	FocusBlock(ctx context.Context, sessionID, blockID string) (int64, error)
	SetBlockLabel(ctx context.Context, sessionID, blockID, label string) (int64, error)
	ListBlocks(ctx context.Context, sessionID string) ([]client.BlockInfo, error)
	CloseBlock(ctx context.Context, sessionID, blockID string) (int64, error)
	Call(ctx context.Context, method string, payload, out any) error
	Events() <-chan client.Event
}

var _ Rex = (*client.Client)(nil)

// Kind is the kind of surface to open.
type Kind int

const (
	// Modal is a floating layer, centred when sized from Cells.
	Modal Kind = iota
	// Drawer is a floating layer flush against one edge that slides in
	// from it and back out on Close.
	Drawer
	// Split is a new tiled block beside Spec.AnchorBlock.
	Split
)

func (k Kind) String() string {
	switch k {
	case Modal:
		return "modal"
	case Drawer:
		return "drawer"
	case Split:
		return "split"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// BlurPolicy says what a surface does when it loses focus. Rex has no
// focus-loss hook a surface could act on server-side, and terminal options
// carry no environment, so the policy is enforced inside the program by
// rexuikit/surface/program; the opener hands the same value to the program
// it launches (in its argv) and the Handle records it.
type BlurPolicy int

const (
	// StayOpen keeps the surface open on blur: the default, for modals and
	// drawers that hold work in progress.
	StayOpen BlurPolicy = iota
	// CloseOnBlur ends the program on blur: for pickers and popovers that
	// are pointless once the user has looked away.
	CloseOnBlur
)

// Edge is the window edge a Drawer is flush against.
type Edge int

const (
	EdgeRight Edge = iota
	EdgeLeft
	EdgeTop
	EdgeBottom
)

func (e Edge) anchor() Anchor {
	switch e {
	case EdgeLeft:
		return LeftEdge
	case EdgeTop:
		return TopEdge
	case EdgeBottom:
		return BottomEdge
	}
	return RightEdge
}

// Spec describes a surface to open.
type Spec struct {
	Kind Kind
	// Argv is the program, run as the block's own process (shell "none"),
	// so the block closes when it exits.
	Argv []string
	Cwd  string
	// Label is the block's label, which Rex shows as the layer's title.
	Label string
	// WindowID is the window to open in; empty means the active window.
	WindowID string
	// Bounds is [x, y, w, h] in window fractions. Zero bounds with a
	// non-zero Cells sizes the layer to its content instead.
	Bounds client.Bounds
	// Cells is the content's natural size, used when Bounds is zero; the
	// layer is placed by Anchor (zero Anchor is centred) for a Modal and by
	// Edge for a Drawer.
	Cells  client.Size
	Anchor Anchor
	Edge   Edge
	Focus  bool
	Blur   BlurPolicy

	// Split only. Direction and Side default to horizontal and after; zero
	// Ratio is the server's 0.5.
	AnchorBlock string
	Direction   string
	Side        string
	Ratio       float64
}

// ErrNotLayer is returned by Resize and Raise on a Split handle.
var ErrNotLayer = errors.New("rexuikit/surface: not a floating layer")

// Option configures a Surface.
type Option func(*Surface)

// WithAnimation sets the drawer animation: frames resize steps, every apart.
// The default is 12 steps at 16ms, which the user confirmed reads as smooth.
// frames < 1 makes a drawer open and close without animating.
func WithAnimation(frames int, every time.Duration) Option {
	return func(s *Surface) { s.frames, s.frameEvery = frames, every }
}

// WithSleep replaces the wait between animation frames (tests).
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(s *Surface) { s.sleep = sleep }
}

// Surface opens surfaces in one Rex session over one connection. It is the
// only reader of the connection's Events channel: the client fails a
// connection whose events go undrained, so a second reader would race this
// one for block_closed.
type Surface struct {
	rex        Rex
	session    string
	frames     int
	frameEvery time.Duration
	sleep      func(context.Context, time.Duration) error

	mu      sync.Mutex
	byBlock map[string]*Handle
	byLayer map[string]*Handle
	lastRev int64
}

// New returns a Surface for sessionID. The connection must already be
// attached to the session (session.attach), which disown_block needs and
// which subscribes it to the events Done relies on.
func New(rex Rex, sessionID string, opts ...Option) *Surface {
	s := &Surface{
		rex:        rex,
		session:    sessionID,
		frames:     12,
		frameEvery: 16 * time.Millisecond,
		sleep:      sleepCtx,
		byBlock:    map[string]*Handle{},
		byLayer:    map[string]*Handle{},
	}
	for _, o := range opts {
		o(s)
	}
	if ev := rex.Events(); ev != nil {
		go s.watch(ev)
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Window reads the cell size of a window (empty id: the active one) from
// session.view and a tiled block's terminal size.
func (s *Surface) Window(ctx context.Context, windowID string) (WindowCells, string, error) {
	v, err := s.rex.View(ctx, s.session)
	if err != nil {
		return WindowCells{}, "", err
	}
	w, ok := findWindow(v, windowID)
	if !ok {
		return WindowCells{}, "", fmt.Errorf("rexuikit/surface: window %q not in session %s", windowID, s.session)
	}
	ref, ok := referenceBlock(w)
	if !ok {
		return WindowCells{}, "", ErrNoTiledBlock
	}
	ts, err := s.rex.TerminalSize(ctx, s.session, ref.blockID)
	if err != nil {
		return WindowCells{}, "", err
	}
	wc, err := windowCellsFrom(ts, ref.rect)
	return wc, w.WindowID, err
}

func (s *Surface) geometry(ctx context.Context, spec Spec) (string, client.Bounds, client.Size, error) {
	wc, wid, err := s.Window(ctx, spec.WindowID)
	if err != nil {
		return "", client.Bounds{}, client.Size{}, err
	}
	b := spec.Bounds
	if b == (client.Bounds{}) {
		if spec.Cells.Columns <= 0 || spec.Cells.Rows <= 0 {
			return "", b, client.Size{}, errors.New("rexuikit/surface: spec has neither Bounds nor Cells")
		}
		a := spec.Anchor
		if spec.Kind == Drawer {
			a = spec.Edge.anchor()
		}
		b, _ = BoundsForCells(wc, spec.Cells.Columns, spec.Cells.Rows, a)
	}
	b = fit(b)
	if b[2] <= 0 || b[3] <= 0 {
		return "", b, client.Size{}, fmt.Errorf("rexuikit/surface: empty bounds %v", b)
	}
	return wid, b, Usable(wc, b), nil
}

func terminal(argv []string, cwd, label string, size *client.Size) client.Layout {
	return client.BlockLayout(client.TerminalFlavor, label, client.TerminalOptions{
		Shell: "none", Command: argv, Cwd: cwd, Size: size,
	})
}

// Open opens a surface. The returned handle's Done closes when its block
// closes (its program exited or someone closed it) or its layer goes away.
func (s *Surface) Open(ctx context.Context, spec Spec) (*Handle, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("rexuikit/surface: spec has no Argv")
	}
	switch spec.Kind {
	case Modal, Drawer:
		return s.openLayer(ctx, spec)
	case Split:
		return s.openSplit(ctx, spec)
	}
	return nil, fmt.Errorf("rexuikit/surface: unknown kind %v", spec.Kind)
}

func (s *Surface) openLayer(ctx context.Context, spec Spec) (*Handle, error) {
	wid, final, size, err := s.geometry(ctx, spec)
	if err != nil {
		return nil, err
	}
	frames := []client.Bounds{final}
	if spec.Kind == Drawer {
		frames = drawerFrames(final, spec.Edge, s.frames)
	}
	res, err := s.rex.NewLayer(ctx, client.NewLayerArgs{
		SessionID: s.session,
		WindowID:  wid,
		Bounds:    frames[0],
		Layout:    terminal(spec.Argv, spec.Cwd, spec.Label, &size),
		Focus:     spec.Focus,
	})
	if err != nil {
		return nil, err
	}
	if len(res.BlockIDs) != 1 {
		return nil, fmt.Errorf("rexuikit/surface: new_layer returned %d blocks, want 1", len(res.BlockIDs))
	}
	h := s.newHandle(spec, res.LayerID, res.BlockIDs[0], wid, final, res.Revision)
	// The creator keeps ownership through the animation: owned, the block
	// stays at the final size it was created with instead of being resized
	// by the app on every frame. The app takes over once it is disowned.
	for _, b := range frames[1:] {
		if err = s.sleep(ctx, s.frameEvery); err != nil {
			break
		}
		if _, err = s.rex.ResizeLayer(ctx, s.session, res.LayerID, b); err != nil {
			break
		}
	}
	if err == nil {
		err = s.disown(ctx, h.block)
	}
	if err != nil {
		cleanup := context.WithoutCancel(ctx)
		_, _ = s.rex.CloseLayer(cleanup, s.session, res.LayerID)
		h.finish()
		return nil, err
	}
	return h, nil
}

func (s *Surface) openSplit(ctx context.Context, spec Spec) (*Handle, error) {
	if spec.AnchorBlock == "" {
		return nil, errors.New("rexuikit/surface: split spec has no AnchorBlock")
	}
	dir, side := spec.Direction, spec.Side
	if dir == "" {
		dir = client.Horizontal
	}
	if side == "" {
		side = client.After
	}
	// No `size` here: a split's cell size depends on split borders the
	// chrome model never sampled (rex-behaviors.md), and a tiled block in a
	// displayed window is sized by the app as soon as it is disowned.
	res, err := s.rex.NewSplit(ctx, client.NewSplitArgs{
		SessionID: s.session,
		AnchorID:  spec.AnchorBlock,
		Direction: dir,
		Side:      side,
		Ratio:     spec.Ratio,
		Layout:    terminal(spec.Argv, spec.Cwd, spec.Label, nil),
		Focus:     spec.Focus,
	})
	if err != nil {
		return nil, err
	}
	if len(res.BlockIDs) != 1 {
		return nil, fmt.Errorf("rexuikit/surface: new_split returned %d blocks, want 1", len(res.BlockIDs))
	}
	h := s.newHandle(spec, "", res.BlockIDs[0], "", client.Bounds{}, res.Revision)
	if err := s.disown(ctx, h.block); err != nil {
		_, _ = s.rex.CloseBlock(context.WithoutCancel(ctx), s.session, h.block)
		h.finish()
		return nil, err
	}
	return h, nil
}

func (s *Surface) disown(ctx context.Context, blockID string) error {
	return s.rex.Call(ctx, "session.disown_block", map[string]any{
		"session_id": s.session,
		"block_ids":  []string{blockID},
	}, nil)
}

// easeOut is a cubic ease-out: fast start, gentle landing, increasing on
// [0, 1].
func easeOut(t float64) float64 { return 1 - math.Pow(1-t, 3) }

// drawerFrames is the slide-in path: frames[0] is a sliver flush against
// edge, then n steps grow it to final, which is the last frame exactly.
// Each frame keeps the far edge fixed so the drawer appears to slide out of
// the window's side.
func drawerFrames(final client.Bounds, edge Edge, n int) []client.Bounds {
	if n < 1 {
		return []client.Bounds{final}
	}
	sliver := 1 / float64(n+1)
	out := make([]client.Bounds, 0, n+1)
	for k := 0; k < n; k++ {
		p := sliver + (1-sliver)*easeOut(float64(k)/float64(n))
		b := final
		switch edge {
		case EdgeLeft:
			b[2] = final[2] * p
		case EdgeTop:
			b[3] = final[3] * p
		case EdgeBottom:
			b[3] = final[3] * p
			b[1] = final[1] + final[3] - b[3]
		default:
			b[2] = final[2] * p
			b[0] = final[0] + final[2] - b[2]
		}
		out = append(out, fit(b))
	}
	return append(out, final)
}

// Handle is an open surface.
type Handle struct {
	s      *Surface
	kind   Kind
	edge   Edge
	blur   BlurPolicy
	layer  string
	block  string
	window string
	rev    int64

	mu     sync.Mutex
	bounds client.Bounds

	done chan struct{}
	once sync.Once
}

func (s *Surface) newHandle(spec Spec, layer, block, window string, b client.Bounds, rev int64) *Handle {
	h := &Handle{
		s: s, kind: spec.Kind, edge: spec.Edge, blur: spec.Blur,
		layer: layer, block: block, window: window, bounds: b, rev: rev,
		done: make(chan struct{}),
	}
	s.mu.Lock()
	s.byBlock[block] = h
	if layer != "" {
		s.byLayer[layer] = h
	}
	s.mu.Unlock()
	return h
}

func (h *Handle) finish() {
	h.once.Do(func() {
		h.s.mu.Lock()
		if h.s.byBlock[h.block] == h {
			delete(h.s.byBlock, h.block)
		}
		if h.layer != "" && h.s.byLayer[h.layer] == h {
			delete(h.s.byLayer, h.layer)
		}
		h.s.mu.Unlock()
		close(h.done)
	})
}

func (h *Handle) Kind() Kind { return h.kind }

// BlockID is the block running the program.
func (h *Handle) BlockID() string { return h.block }

// LayerID is the floating layer, empty for a Split.
func (h *Handle) LayerID() string { return h.layer }

// WindowID is the window a layer was opened in, empty for a Split.
func (h *Handle) WindowID() string { return h.window }

// Blur is the policy the surface was opened with.
func (h *Handle) Blur() BlurPolicy { return h.blur }

// Bounds is the layer's current bounds, zero for a Split.
func (h *Handle) Bounds() client.Bounds {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bounds
}

// Done is closed once the surface is gone: its block closed, its layer was
// removed, Close returned, or the connection ended (after which nothing about
// the surface can be observed).
func (h *Handle) Done() <-chan struct{} { return h.done }

// Close closes the surface: a Drawer slides out first. A surface that is
// already gone is not an error.
func (h *Handle) Close(ctx context.Context) error {
	defer h.finish()
	select {
	case <-h.done:
		return nil
	default:
	}
	var err error
	switch {
	case h.layer == "":
		_, err = h.s.rex.CloseBlock(ctx, h.s.session, h.block)
	default:
		if h.kind == Drawer {
			frames := drawerFrames(h.Bounds(), h.edge, h.s.frames)
			for i := len(frames) - 2; i >= 0; i-- {
				if h.s.sleep(ctx, h.s.frameEvery) != nil {
					break
				}
				if _, e := h.s.rex.ResizeLayer(ctx, h.s.session, h.layer, frames[i]); e != nil {
					break
				}
			}
		}
		_, err = h.s.rex.CloseLayer(context.WithoutCancel(ctx), h.s.session, h.layer)
	}
	if errors.Is(err, client.ErrNotFound) {
		return nil
	}
	return err
}

// Resize moves or resizes a layer. Bounds that would leave the window are
// pulled back inside it.
func (h *Handle) Resize(ctx context.Context, b client.Bounds) error {
	if h.layer == "" {
		return ErrNotLayer
	}
	b = fit(b)
	if _, err := h.s.rex.ResizeLayer(ctx, h.s.session, h.layer, b); err != nil {
		return err
	}
	h.mu.Lock()
	h.bounds = b
	h.mu.Unlock()
	return nil
}

// Raise brings a layer in front of the window's other floating layers.
func (h *Handle) Raise(ctx context.Context) error {
	if h.layer == "" {
		return ErrNotLayer
	}
	_, err := h.s.rex.RaiseLayer(ctx, h.s.session, h.layer, "")
	return err
}

// watch turns session events into Done. block_closed names the block
// directly. A layer has no closed event of its own, so on each
// session_view_changed newer than the last view read, open layers are
// checked against a fresh view. Events can arrive out of revision order
// ("### `session_view_changed`"), hence the revision guard; a handle is only
// judged against a view at or after the revision that created it, so a view
// read just before its layer existed cannot end it.
func (s *Surface) watch(events <-chan client.Event) {
	defer s.finishAll()
	for ev := range events {
		se, ok, err := ev.Session()
		if !ok || err != nil || se.SessionID != s.session {
			continue
		}
		switch se.Type {
		case "block_closed":
			var p struct {
				BlockID string `json:"block_id"`
			}
			if json.Unmarshal(se.Payload, &p) == nil {
				s.mu.Lock()
				h := s.byBlock[p.BlockID]
				s.mu.Unlock()
				if h != nil {
					h.finish()
				}
			}
		case "session_view_changed":
			var p struct {
				Revision int64 `json:"revision"`
			}
			if json.Unmarshal(se.Payload, &p) == nil {
				s.checkLayers(p.Revision)
			}
		}
	}
}

func (s *Surface) checkLayers(rev int64) {
	s.mu.Lock()
	if rev <= s.lastRev || len(s.byLayer) == 0 {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := s.rex.View(ctx, s.session)
	if err != nil {
		return
	}
	present := map[string]bool{}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			present[l.LayerID] = true
		}
	}
	var gone []*Handle
	s.mu.Lock()
	s.lastRev = max(s.lastRev, v.Revision)
	for id, h := range s.byLayer {
		if !present[id] && v.Revision >= h.rev {
			gone = append(gone, h)
		}
	}
	s.mu.Unlock()
	for _, h := range gone {
		h.finish()
	}
}

func (s *Surface) finishAll() {
	s.mu.Lock()
	var all []*Handle
	for _, h := range s.byBlock {
		all = append(all, h)
	}
	s.mu.Unlock()
	for _, h := range all {
		h.finish()
	}
}
