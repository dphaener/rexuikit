package surface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dphaener/rexuikit/client"
)

// Pre-warm pool. A warm block is a detached block whose program has already
// started and drawn its first frame at the right size; placing it into a
// layer shows that frame at once instead of waiting ~85ms+ for a cold start
// (rex-behaviors.md, measured latencies). The labels are the pool's only
// index: Rex lists detached blocks with their label and placement, and both
// the Go side here and forge's generated Lua find warm blocks by them.

// WarmPrefix starts the label of every warm block. It is the one spelling of
// it: forge's install code closes blocks by it, and forge's generated Lua
// gets it injected at install time.
const WarmPrefix = "forge-warm:"

// WarmActionPrefix starts the label of every warm block for action, whatever
// binary built it.
func WarmActionPrefix(action string) string {
	return WarmPrefix + action + ":"
}

// WarmLabel is the label of a warm block for action built by buildID.
// buildID ties the block to the binary that started it, so a rebuilt binary
// never places a form drawn by the old one.
func WarmLabel(action, buildID string) string {
	return WarmActionPrefix(action) + buildID
}

// IsWarm reports whether b is a warm block waiting to be placed: a warm
// label AND detached placement. A placed block can keep its warm label for
// a moment before Place relabels it, and that block is the form on screen.
func IsWarm(b client.BlockInfo) bool {
	return b.Placement == client.Detached && strings.HasPrefix(b.Label, WarmPrefix)
}

// FindWarm returns the oldest detached block warmed for action. Placement is
// checked as well as the label because a placed block keeps its label until
// Place relabels it; a lookup in that window must not pick the form on
// screen. Block ids are `block:<uuidv7>`, so the lexically smallest id is the
// oldest block (rex-behaviors.md).
func FindWarm(blocks []client.BlockInfo, action string) (client.BlockInfo, bool) {
	prefix := WarmActionPrefix(action)
	var best client.BlockInfo
	found := false
	for _, b := range blocks {
		if !IsWarm(b) || !strings.HasPrefix(b.Label, prefix) {
			continue
		}
		if !found || b.BlockID < best.BlockID {
			best, found = b, true
		}
	}
	return best, found
}

// WarmBlock is a detached block started by Warm.
type WarmBlock struct {
	BlockID string
	Label   string
	// Size is the cell size the program was started at.
	Size client.Size
	// WindowID is where Place opens the layer; empty means the active
	// window at Place time.
	WindowID string
}

// Warm starts spec's program in a detached block at the usable size of
// spec's bounds (or Cells), so its first frame is already the frame Place
// will show. The block is disowned at once: a detached block keeps the size
// it was created with, and once placed the app can size it.
func (s *Surface) Warm(ctx context.Context, spec Spec) (WarmBlock, error) {
	if len(spec.Argv) == 0 {
		return WarmBlock{}, errors.New("rexuikit/surface: spec has no Argv")
	}
	_, _, size, err := s.geometry(ctx, spec)
	if err != nil {
		return WarmBlock{}, err
	}
	id, err := s.rex.NewBlock(ctx, client.NewBlockArgs{
		SessionID: s.session,
		Flavor:    client.TerminalFlavor,
		Label:     spec.Label,
		Options:   client.TerminalOptions{Shell: "none", Command: spec.Argv, Cwd: spec.Cwd, Size: &size},
	})
	if err != nil {
		return WarmBlock{}, err
	}
	if err := s.disown(ctx, id); err != nil {
		_, _ = s.rex.CloseBlock(context.WithoutCancel(ctx), s.session, id)
		return WarmBlock{}, err
	}
	return WarmBlock{BlockID: id, Label: spec.Label, Size: size, WindowID: spec.WindowID}, nil
}

// placeholderArgv holds the new layer open for the few milliseconds it takes
// to move the warm block in beside it.
var placeholderArgv = []string{"/bin/sleep", "30"}

// Place shows a warm block in a new floating layer at bounds. Rex's
// float_block refuses detached blocks (not_found), so placement is the
// sequence the live tests proved: new_layer with a placeholder, move_block
// the warm block beside it, block.close the placeholder, which leaves the
// warm block alone in the layer (~10-15ms in-server). The block is then
// relabelled to title, focused when asked (a detached block does not take
// focus when moved) and disowned.
//
// title is what Rex's title bar shows for the layer: the label of the
// layer's only block. It is the form's own title, the same a cold open
// uses, so a pre-warmed form reads the same as a cold one. The plan for
// this package named a fixed `forge-live:<action>` label (LiveLabel)
// instead; the user chose the readable title (decisions.md, "Readable
// title"), so there is no LiveLabel. Nothing looks a
// placed block up by it: FindWarm needs the warm prefix AND detached
// placement, and the relabel also drops the warm prefix, so a placed form
// can never be picked again whatever its title says. An empty title is
// refused before anything is placed, because the block would keep its
// machine label in the title bar.
func (s *Surface) Place(ctx context.Context, wb WarmBlock, bounds client.Bounds, title string, focus bool) (*Handle, error) {
	if title == "" {
		return nil, errors.New("rexuikit/surface: Place needs a title for the layer")
	}
	if strings.HasPrefix(title, WarmPrefix) {
		return nil, fmt.Errorf("rexuikit/surface: title %q starts with the warm prefix %q", title, WarmPrefix)
	}
	bounds = fit(bounds)
	res, err := s.rex.NewLayer(ctx, client.NewLayerArgs{
		SessionID: s.session,
		WindowID:  wb.WindowID,
		Bounds:    bounds,
		Layout:    terminal(placeholderArgv, "", title, nil),
		Focus:     focus,
	})
	if err != nil {
		return nil, err
	}
	if len(res.BlockIDs) != 1 {
		return nil, fmt.Errorf("rexuikit/surface: new_layer returned %d blocks, want 1", len(res.BlockIDs))
	}
	placeholder := res.BlockIDs[0]
	cleanup := context.WithoutCancel(ctx)
	if _, err := s.rex.MoveBlock(ctx, client.MoveBlockArgs{
		SessionID: s.session, BlockID: wb.BlockID, AnchorBlockID: placeholder,
	}); err != nil {
		_, _ = s.rex.CloseLayer(cleanup, s.session, res.LayerID)
		return nil, err
	}
	rev, err := s.rex.CloseBlock(ctx, s.session, placeholder)
	if err != nil {
		_, _ = s.rex.CloseLayer(cleanup, s.session, res.LayerID)
		return nil, err
	}
	h := s.newHandle(Spec{Kind: Modal}, res.LayerID, wb.BlockID, wb.WindowID, bounds, max(rev, res.Revision))
	fail := func(err error) (*Handle, error) {
		_, _ = s.rex.CloseLayer(cleanup, s.session, res.LayerID)
		h.finish()
		return nil, err
	}
	if _, err := s.rex.SetBlockLabel(ctx, s.session, wb.BlockID, title); err != nil {
		return fail(err)
	}
	if focus {
		if _, err := s.rex.FocusBlock(ctx, s.session, wb.BlockID); err != nil {
			return fail(err)
		}
	}
	if err := s.disown(ctx, wb.BlockID); err != nil {
		return fail(err)
	}
	return h, nil
}

// PlacementContext tells a warm program where it was placed. A warm program
// starts before anyone knows which pane will open it, so the placer (forge's
// generated Lua) writes this file just before placement and the program reads
// it when it gets focus.
type PlacementContext struct {
	OpenerBlockID string `json:"opener_block_id"`
	Cwd           string `json:"cwd"`
	SessionID     string `json:"session_id"`
	WindowID      string `json:"window_id"`
	// Timestamp is Unix seconds: config Lua has only os.time, which has no
	// finer clock (rex-behaviors.md).
	Timestamp int64 `json:"timestamp"`
	// WarmBlockID is the warm block being placed. Every warm program for
	// the action polls the same file, so a program accepts a context only
	// when this is its own block (Rex sets REX_BLOCK to each block's id).
	// Empty in a context written by an older placer.
	WarmBlockID string `json:"warm_block_id,omitempty"`
}

// For reports whether a warm program in block self may act on pc. A context
// naming another block is that block's placement. An empty self (the block
// is unknown) or an empty WarmBlockID (an older placer) accepts any.
func (pc PlacementContext) For(self string) bool {
	return self == "" || pc.WarmBlockID == "" || pc.WarmBlockID == self
}

// WriteContext writes pc to path atomically: a temp file in the same
// directory renamed over path, so a warm program polling the file never
// reads half of it.
func WriteContext(path string, pc PlacementContext) error {
	data, err := json.Marshal(pc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".ctx-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ReadContext reads a context file written by WriteContext or the Lua.
func ReadContext(path string) (PlacementContext, error) {
	var pc PlacementContext
	data, err := os.ReadFile(path)
	if err != nil {
		return pc, err
	}
	if err := json.Unmarshal(data, &pc); err != nil {
		return pc, fmt.Errorf("rexuikit/surface: decode %s: %w", path, err)
	}
	return pc, nil
}
