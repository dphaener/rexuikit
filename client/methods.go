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

// ProcessMethod is the terminal block's process method, which reports the
// program the terminal started ("### `com.superlogical.terminal.process`").
const ProcessMethod = "com.superlogical.terminal.process"

// ProcessInfo is one process in ProcessResult. Optional fields are empty
// when the server left them out.
type ProcessInfo struct {
	PID         int    `json:"pid"`
	UserID      int    `json:"user_id"`
	User        string `json:"user,omitempty"`
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	InvokedPath string `json:"invoked_path,omitempty"`
	Argv0       string `json:"argv0,omitempty"`
	Script      string `json:"script,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

// ProcessExit is how a terminal's program ended.
type ProcessExit struct {
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ProcessResult is com.superlogical.terminal.process's result. Child is the
// process the terminal started: the shell, or under shell "none" the
// program itself. Nil fields were null or left out.
type ProcessResult struct {
	LastExit   *ProcessExit  `json:"last_exit"`
	Child      *ProcessInfo  `json:"child,omitempty"`
	Foreground *ProcessInfo  `json:"foreground,omitempty"`
	Ancestors  []ProcessInfo `json:"ancestors,omitempty"`
	Home       string        `json:"home,omitempty"`
}

// BlockProcess calls com.superlogical.terminal.process on a terminal block.
func (c *Client) BlockProcess(ctx context.Context, sessionID, blockID string) (ProcessResult, error) {
	var r ProcessResult
	err := c.BlockMethod(ctx, sessionID, blockID, ProcessMethod, nil, &r)
	return r, err
}

// CreateSessionArgs are session.create's arguments as rexuikit uses them:
// a session whose first window holds one terminal. The method itself takes
// no cwd, so Cwd goes into the first window's TerminalOptions. Empty
// WindowLabel leaves the server's default window label.
type CreateSessionArgs struct {
	Label       string
	WindowLabel string
	Cwd         string
}

// CreateSessionResult is session.create's result.
type CreateSessionResult struct {
	SessionID      string            `json:"session_id"`
	InitialWindows []NewWindowResult `json:"initial_windows"`
}

// FirstWindowID is the first window's id, or empty if none was created.
func (r CreateSessionResult) FirstWindowID() string {
	if len(r.InitialWindows) == 0 {
		return ""
	}
	return r.InitialWindows[0].WindowID
}

// FirstBlockID is the first window's first block id, or empty if none.
func (r CreateSessionResult) FirstBlockID() string {
	if len(r.InitialWindows) == 0 || len(r.InitialWindows[0].BlockIDs) == 0 {
		return ""
	}
	return r.InitialWindows[0].BlockIDs[0]
}

// CreateSession calls session.create ("### `session.create`") with one
// initial window holding a terminal. The call owns the new blocks but does
// not attach the connection to the session: Attach before calling methods
// that name it. The server applies the label only when non-empty and picks
// one otherwise.
func (c *Client) CreateSession(ctx context.Context, a CreateSessionArgs) (CreateSessionResult, error) {
	type window struct {
		Label  string `json:"window_label,omitempty"`
		Layout Layout `json:"layout"`
	}
	var opts any
	if a.Cwd != "" {
		opts = TerminalOptions{Cwd: a.Cwd}
	}
	var r CreateSessionResult
	err := c.Call(ctx, "session.create", struct {
		Label          string   `json:"label,omitempty"`
		InitialWindows []window `json:"initial_windows"`
	}{a.Label, []window{{a.WindowLabel, BlockLayout(TerminalFlavor, "", opts)}}}, &r)
	return r, err
}

// DestroySession calls session.destroy ("### `session.destroy`"), closing
// every block in the session. The connection must be attached to it
// (Attach or AttachAll) or the call fails with conflict.
func (c *Client) DestroySession(ctx context.Context, sessionID string) error {
	return c.Call(ctx, "session.destroy", sessionArgs{sessionID}, nil)
}

// FocusWindow calls session.focus_window ("### `session.focus_window`"). A
// window outside the session fails with not_found.
func (c *Client) FocusWindow(ctx context.Context, sessionID, windowID string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.focus_window", struct {
		SessionID string `json:"session_id"`
		WindowID  string `json:"window_id"`
	}{sessionID, windowID}, &r)
	return r.Revision, err
}

// SetWindowLabel calls session.set_window_label
// ("### `session.set_window_label`"). An empty label restores the default.
func (c *Client) SetWindowLabel(ctx context.Context, sessionID, windowID, label string) (int64, error) {
	var r revisionResult
	err := c.Call(ctx, "session.set_window_label", struct {
		SessionID string `json:"session_id"`
		WindowID  string `json:"window_id"`
		Label     string `json:"label"`
	}{sessionID, windowID, label}, &r)
	return r.Revision, err
}

// EventScope is session.attach_all's event_scope.
type EventScope string

const (
	// ScopeSession sends every session's events except block_event.
	ScopeSession EventScope = "session"
	// ScopeAll also sends block_event and the client events.
	ScopeAll EventScope = "all"
)

// AttachAll calls session.attach_all ("### `session.attach_all`"). The
// connection may then call methods on every session and receives events
// per scope, but does not join any session: it cannot own or disown
// blocks (use Attach for that). The returned list is a snapshot; apply
// events with a seq above the response's. Calling again replaces the scope.
func (c *Client) AttachAll(ctx context.Context, scope EventScope) ([]SessionInfo, error) {
	var r struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	err := c.Call(ctx, "session.attach_all", struct {
		EventScope EventScope `json:"event_scope"`
	}{scope}, &r)
	return r.Sessions, err
}

// ClientInfo is what a client says about itself in ClientEntry.
type ClientInfo struct {
	Kind     string `json:"kind,omitempty"`
	Platform string `json:"platform,omitempty"`
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Label    string `json:"label,omitempty"`
}

// ClientPrincipal is how the server identified a client. PID is zero when
// the server reported none.
type ClientPrincipal struct {
	Kind      string `json:"kind"`
	Transport string `json:"transport"`
	Display   string `json:"display"`
	UID       *int   `json:"uid,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

// Connection states in ClientEntry. The spec says to treat a missing or
// unknown value as connected.
const (
	ClientConnected = "connected"
	ClientLost      = "lost"
)

// ClientEntry is one client in client.list's result.
type ClientEntry struct {
	ClientID        string          `json:"client_id"`
	Info            ClientInfo      `json:"info"`
	Principal       ClientPrincipal `json:"principal"`
	ConnectedAt     string          `json:"connected_at"`
	ConnectionState string          `json:"connection_state,omitempty"`
	SessionIDs      []string        `json:"session_ids,omitempty"`
}

// Lost reports whether the client's connection dropped and it may still
// reconnect.
func (e ClientEntry) Lost() bool { return e.ConnectionState == ClientLost }

// ListClients calls client.list ("### `client.list`"). Empty kinds lists
// every client, including short-lived ones; otherwise only those of the
// named kinds, such as "app". A non-empty sessionID counts only clients
// attached with Attach, not those that called AttachAll.
func (c *Client) ListClients(ctx context.Context, sessionID string, kinds ...string) ([]ClientEntry, error) {
	var r struct {
		Clients []ClientEntry `json:"clients"`
	}
	err := c.Call(ctx, "client.list", struct {
		SessionID string   `json:"session_id,omitempty"`
		Kinds     []string `json:"kinds,omitempty"`
	}{sessionID, kinds}, &r)
	return r.Clients, err
}

// PerformArgs are client.perform's arguments. Args, if set, must marshal to
// a JSON object. Zero Origin means the server's "api"; zero TimeoutMS its
// 10000.
type PerformArgs struct {
	ClientID  string `json:"client_id"`
	Action    string `json:"action"`
	Args      any    `json:"args,omitempty"`
	Origin    string `json:"origin,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// PerformResult is client.perform's result: Result is the action's answer
// (empty when it returned null) and PerformedBy the client that ran it.
type PerformResult struct {
	Result      json.RawMessage `json:"result,omitempty"`
	PerformedBy string          `json:"performed_by"`
}

// Perform calls client.perform ("### `client.perform`"). A missing client
// is not_found, an undeclared action not_supported, a client that declines
// refused (Remote Control off, for the app's session.select), a busy or
// reconnecting one client_unavailable, and silence timeout.
func (c *Client) Perform(ctx context.Context, a PerformArgs) (PerformResult, error) {
	var r PerformResult
	err := c.Call(ctx, "client.perform", a, &r)
	return r, err
}

// Diagnostic is one problem found in the configuration file.
type Diagnostic struct {
	Severity string `json:"severity"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
	Action   string `json:"action,omitempty"`
	Key      string `json:"key,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// ConfigReloadResult is server.config_reload's result.
type ConfigReloadResult struct {
	Generation  int          `json:"generation"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ConfigReload calls server.config_reload ("### `server.config_reload`").
// A config that fails to load is not an error: the server keeps the last
// good one and reports it in Diagnostics. The server accepts the call only
// over its local unix socket and answers anything else with status 403.
func (c *Client) ConfigReload(ctx context.Context) (ConfigReloadResult, error) {
	var r ConfigReloadResult
	err := c.Call(ctx, "server.config_reload", nil, &r)
	return r, err
}
