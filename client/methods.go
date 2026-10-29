package client

import (
	"context"
	"encoding/json"
)

// Typed wrappers for the methods rexuikit uses. Field names and optionality
// follow "## Method Reference" in docs/rex-api/llms-r7.md; each wrapper names
// its section. Optional arguments use omitempty so the server applies its
// documented default.

// TerminalFlavor is the terminal block flavor, the only one rexuikit creates.
const TerminalFlavor = "com.superlogical.terminal.shell"

// Directions and sides for splits ("### `session.new_split`").
const (
	Horizontal = "horizontal"
	Vertical   = "vertical"
	Before     = "before"
	After      = "after"
)

// Bounds is [x, y, w, h] as fractions of the window's width and height.
type Bounds [4]float64

type Size struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// TerminalOptions are a terminal block's creation options. The r7 snapshot
// does not spell this schema out (block_creator.list serves it live); these
// fields are the ones observed working during planning probes: Shell "none"
// with Command runs Command as the block's own program (Shell "none" without
// Command tries to exec "none"), and Size sets the starting size, without
// which a block starts at 80x24.
type TerminalOptions struct {
	Shell   string   `json:"shell,omitempty"`
	Command []string `json:"command,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Size    *Size    `json:"size,omitempty"`
}

// Layout is a node of a layout to create: exactly one of Block or Split.
type Layout struct {
	Block *BlockSpec `json:"block,omitempty"`
	Split *SplitSpec `json:"split,omitempty"`
}

// BlockSpec starts one new block. Options is passed to the flavor's creator
// unchanged; for TerminalFlavor use TerminalOptions.
type BlockSpec struct {
	Flavor  string `json:"flavor"`
	Options any    `json:"options,omitempty"`
	Label   string `json:"label,omitempty"`
}

// SplitSpec divides a node in two. Zero Ratio means the server's 0.5.
type SplitSpec struct {
	Direction string  `json:"direction"`
	Before    Layout  `json:"before"`
	After     Layout  `json:"after"`
	Ratio     float64 `json:"ratio,omitempty"`
}

func BlockLayout(flavor, label string, options any) Layout {
	return Layout{Block: &BlockSpec{Flavor: flavor, Label: label, Options: options}}
}

// Version is server.version's result ("### `server.version`").
type Version struct {
	RexVersion     string `json:"rex_version"`
	Platform       string `json:"platform"`
	Commit         string `json:"commit,omitempty"`
	BuiltAt        string `json:"built_at,omitempty"`
	Dirty          bool   `json:"dirty"`
	APIRevision    int    `json:"api_revision,omitempty"`
	APIMinRevision int    `json:"api_min_revision,omitempty"`
}

func (c *Client) ServerVersion(ctx context.Context) (Version, error) {
	var v Version
	err := c.Call(ctx, "server.version", nil, &v)
	return v, err
}

type SessionInfo struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

// ListSessions calls session.list ("### `session.list`"), in creation order.
func (c *Client) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	var r struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	err := c.Call(ctx, "session.list", nil, &r)
	return r.Sessions, err
}

// View is a session's structure ("### `session.view`"). It holds only
// placed blocks; ListBlocks also lists detached ones.
type View struct {
	Revision       int64    `json:"revision"`
	SessionID      string   `json:"session_id"`
	Label          string   `json:"label,omitempty"`
	ActiveWindowID string   `json:"active_window_id,omitempty"`
	Windows        []Window `json:"windows"`
}

type Window struct {
	WindowID       string  `json:"window_id"`
	Label          string  `json:"label,omitempty"`
	FocusedBlockID string  `json:"focused_block_id,omitempty"`
	ZoomedBlockID  string  `json:"zoomed_block_id,omitempty"`
	Layers         []Layer `json:"layers"`
}

// Layer kinds. The spec says to treat an unknown kind as floating.
const (
	LayerTiled    = "tiled"
	LayerFloating = "floating"
)

// Layer is a window's layer; the first is the tiled layout.
type Layer struct {
	LayerID string      `json:"layer_id"`
	Kind    string      `json:"kind"`
	Bounds  Bounds      `json:"bounds"`
	Layout  *LayoutNode `json:"layout"`
	Blocks  []ViewBlock `json:"blocks"`
}

// LayoutNode is a node of an existing layout: a block or a split.
type LayoutNode struct {
	Rect    Bounds     `json:"rect"`
	BlockID string     `json:"block_id,omitempty"`
	Split   *ViewSplit `json:"split,omitempty"`
}

type ViewSplit struct {
	SplitID   string     `json:"split_id"`
	Direction string     `json:"direction"`
	Ratio     float64    `json:"ratio"`
	Before    LayoutNode `json:"before"`
	After     LayoutNode `json:"after"`
}

type ViewBlock struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label,omitempty"`
	CreatorName string `json:"creator_name,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	HostID      string `json:"host_id,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// Attachment is session.attach's result plus the response seq. Apply View
// first, then only events with a seq above Seq ("### Combining Results With
// Events").
type Attachment struct {
	ClientID string `json:"client_id"`
	View     View   `json:"view"`
	Seq      int64  `json:"-"`
}

type sessionArgs struct {
	SessionID string `json:"session_id"`
}

// Attach calls session.attach ("### `session.attach`"). Most methods naming
// a session fail with conflict until the connection is attached to it.
func (c *Client) Attach(ctx context.Context, sessionID string) (Attachment, error) {
	var a Attachment
	seq, err := c.CallSeq(ctx, "session.attach", sessionArgs{sessionID}, &a)
	a.Seq = seq
	return a, err
}

// Detach calls session.detach ("### `session.detach`").
func (c *Client) Detach(ctx context.Context, sessionID string) error {
	return c.Call(ctx, "session.detach", sessionArgs{sessionID}, nil)
}

// View calls session.view ("### `session.view`").
func (c *Client) View(ctx context.Context, sessionID string) (View, error) {
	var v View
	err := c.Call(ctx, "session.view", sessionArgs{sessionID}, &v)
	return v, err
}

type revisionResult struct {
	Revision int64 `json:"revision"`
}

// NewWindowArgs are session.new_window's arguments. Nil Focus means the
// server default, which is true.
type NewWindowArgs struct {
	SessionID string `json:"session_id"`
	Label     string `json:"window_label,omitempty"`
	Layout    Layout `json:"layout"`
	Focus     *bool  `json:"focus,omitempty"`
}

type NewWindowResult struct {
	WindowID string   `json:"window_id"`
	BlockIDs []string `json:"block_ids"`
	Revision int64    `json:"revision"`
}

// NewWindow calls session.new_window ("### `session.new_window`").
func (c *Client) NewWindow(ctx context.Context, a NewWindowArgs) (NewWindowResult, error) {
	var r NewWindowResult
	err := c.Call(ctx, "session.new_window", a, &r)
	return r, err
}

// CloseWindow calls session.close_window ("### `session.close_window`"),
// which closes every block in the window. Empty windowID closes the active
// window.
func (c *Client) CloseWindow(ctx context.Context, sessionID, windowID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.close_window", struct {
		SessionID string `json:"session_id"`
		WindowID  string `json:"window_id,omitempty"`
	}{sessionID, windowID}, &r)
	return r.Revision, err
}

// NewSplitArgs are session.new_split's arguments. Send AnchorID or WindowID,
// not both; with neither, the active window's focused block is split.
type NewSplitArgs struct {
	SessionID string  `json:"session_id"`
	AnchorID  string  `json:"anchor_id,omitempty"`
	WindowID  string  `json:"window_id,omitempty"`
	Direction string  `json:"direction"`
	Side      string  `json:"side"`
	Ratio     float64 `json:"ratio,omitempty"`
	Layout    Layout  `json:"layout"`
	Focus     bool    `json:"focus,omitempty"`
}

type NewSplitResult struct {
	AnchorID string   `json:"anchor_id"`
	BlockIDs []string `json:"block_ids"`
	SplitIDs []string `json:"split_ids"`
	Revision int64    `json:"revision"`
}

// NewSplit calls session.new_split ("### `session.new_split`").
func (c *Client) NewSplit(ctx context.Context, a NewSplitArgs) (NewSplitResult, error) {
	var r NewSplitResult
	err := c.Call(ctx, "session.new_split", a, &r)
	return r, err
}

// SetBlockLabel calls session.set_block_label
// ("### `session.set_block_label`"). An empty label restores the default.
func (c *Client) SetBlockLabel(ctx context.Context, sessionID, blockID, label string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.set_block_label", struct {
		SessionID string `json:"session_id"`
		BlockID   string `json:"block_id"`
		Label     string `json:"label"`
	}{sessionID, blockID, label}, &r)
	return r.Revision, err
}

// NewLayerArgs are session.new_layer's arguments. Empty WindowID means the
// active window.
type NewLayerArgs struct {
	SessionID string `json:"session_id"`
	WindowID  string `json:"window_id,omitempty"`
	Bounds    Bounds `json:"bounds"`
	Layout    Layout `json:"layout"`
	Focus     bool   `json:"focus,omitempty"`
}

type NewLayerResult struct {
	LayerID  string   `json:"layer_id"`
	BlockIDs []string `json:"block_ids"`
	Revision int64    `json:"revision"`
}

// NewLayer calls session.new_layer ("### `session.new_layer`").
func (c *Client) NewLayer(ctx context.Context, a NewLayerArgs) (NewLayerResult, error) {
	var r NewLayerResult
	err := c.Call(ctx, "session.new_layer", a, &r)
	return r, err
}

type layerArgs struct {
	SessionID string `json:"session_id"`
	LayerID   string `json:"layer_id"`
}

// ResizeLayer calls session.resize_layer ("### `session.resize_layer`"). It
// is not an ordered method, so successive animation frames sent without
// waiting may apply out of order.
func (c *Client) ResizeLayer(ctx context.Context, sessionID, layerID string, b Bounds) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.resize_layer", struct {
		layerArgs
		Bounds Bounds `json:"bounds"`
	}{layerArgs{sessionID, layerID}, b}, &r)
	return r.Revision, err
}

// CloseLayer calls session.close_layer ("### `session.close_layer`"). If the
// layer had focus, focus returns to the layer that had it before.
func (c *Client) CloseLayer(ctx context.Context, sessionID, layerID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.close_layer", layerArgs{sessionID, layerID}, &r)
	return r.Revision, err
}

// RaiseLayer calls session.raise_layer ("### `session.raise_layer`"). Empty
// relativeLayerID moves the layer to the front.
func (c *Client) RaiseLayer(ctx context.Context, sessionID, layerID, relativeLayerID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.raise_layer", struct {
		layerArgs
		RelativeLayerID string `json:"relative_layer_id,omitempty"`
	}{layerArgs{sessionID, layerID}, relativeLayerID}, &r)
	return r.Revision, err
}

type NewBlockArgs struct {
	SessionID string `json:"session_id"`
	Flavor    string `json:"flavor"`
	Options   any    `json:"options,omitempty"`
	Label     string `json:"label,omitempty"`
}

// NewBlock calls session.new_block ("### `session.new_block`"): the block
// starts detached and owned by this client. It returns the block id.
func (c *Client) NewBlock(ctx context.Context, a NewBlockArgs) (string, error) {
	var r struct {
		BlockID string `json:"block_id"`
	}
	err := c.Call(ctx, "session.new_block", a, &r)
	return r.BlockID, err
}

// MoveBlockArgs are session.move_block's arguments. Empty Direction and Side
// mean the server's horizontal and after.
type MoveBlockArgs struct {
	SessionID     string  `json:"session_id"`
	BlockID       string  `json:"block_id"`
	AnchorBlockID string  `json:"anchor_block_id"`
	Direction     string  `json:"direction,omitempty"`
	Side          string  `json:"side,omitempty"`
	Ratio         float64 `json:"ratio,omitempty"`
}

type MoveBlockResult struct {
	CreatedSplitID string `json:"created_split_id"`
	RemovedSplitID string `json:"removed_split_id,omitempty"`
	Revision       int64  `json:"revision"`
}

// MoveBlock calls session.move_block ("### `session.move_block`"). It places
// a detached block beside an anchor; a move that empties a floating layer
// removes the layer.
func (c *Client) MoveBlock(ctx context.Context, a MoveBlockArgs) (MoveBlockResult, error) {
	var r MoveBlockResult
	err := c.Call(ctx, "session.move_block", a, &r)
	return r, err
}

type blockArgs struct {
	SessionID string `json:"session_id"`
	BlockID   string `json:"block_id"`
}

// FocusBlock calls session.focus_block ("### `session.focus_block`"). A
// detached block cannot be focused (not_found).
func (c *Client) FocusBlock(ctx context.Context, sessionID, blockID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.focus_block", blockArgs{sessionID, blockID}, &r)
	return r.Revision, err
}

// Block placements in BlockInfo. The spec says to treat an unknown value as
// detached.
const (
	Placed   = "placed"
	Detached = "detached"
)

type BlockInfo struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label"`
	CreatorName string `json:"creator_name,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	HostID      string `json:"host_id,omitempty"`
	Placement   string `json:"placement"`
	WindowID    string `json:"window_id,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// ListBlocks calls session.list_blocks ("### `session.list_blocks`"):
// every block including detached ones, sorted by block id.
func (c *Client) ListBlocks(ctx context.Context, sessionID string) ([]BlockInfo, error) {
	var r struct {
		Blocks []BlockInfo `json:"blocks"`
	}
	err := c.Call(ctx, "session.list_blocks", sessionArgs{sessionID}, &r)
	return r.Blocks, err
}

// CloseBlock calls block.close ("### `block.close`") on one block, placed or
// detached, ending its program.
func (c *Client) CloseBlock(ctx context.Context, sessionID, blockID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "block.close", blockArgs{sessionID, blockID}, &r)
	return r.Revision, err
}

// BlockMethod calls a block's own method, such as
// com.superlogical.terminal.size, with its arguments under args ("## Calling
// Block Methods"). Nil args sends an empty object, which every block
// method's object schema accepts.
func (c *Client) BlockMethod(ctx context.Context, sessionID, blockID, method string, args, out any) error {
	if args == nil {
		args = json.RawMessage(`{}`)
	}
	return c.Call(ctx, method, struct {
		blockArgs
		Args any `json:"args"`
	}{blockArgs{sessionID, blockID}, args}, out)
}

// TerminalSize is com.superlogical.terminal.size's result.
type TerminalSize struct {
	Columns      int           `json:"columns"`
	Rows         int           `json:"rows"`
	CellWidthPx  int           `json:"cell_width_px"`
	CellHeightPx int           `json:"cell_height_px"`
	Owner        string        `json:"owner,omitempty"`
	Desired      []DesiredSize `json:"desired"`
}

type DesiredSize struct {
	ClientID  string `json:"client_id"`
	Columns   int    `json:"columns"`
	Rows      int    `json:"rows"`
	UpdatedAt string `json:"updated_at"`
}

func (c *Client) TerminalSize(ctx context.Context, sessionID, blockID string) (TerminalSize, error) {
	var s TerminalSize
	err := c.BlockMethod(ctx, sessionID, blockID, "com.superlogical.terminal.size", nil, &s)
	return s, err
}
