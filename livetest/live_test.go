//go:build rexlive

package livetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dphaener/rexuikit/client"
)

const (
	// labelPrefix starts the label of every block the suite creates, so a
	// leftover from a crashed run can be found and closed without touching
	// anything of the user's.
	labelPrefix = "rexuikit-livetest"
	behaviorsMD = "../docs/rex-behaviors.md"
	versionFile = "../docs/rex-api/VERSION"

	// defaultCols x defaultRows is the size a terminal block starts at when
	// no size is passed and nobody has requested one.
	defaultCols = 80
	defaultRows = 24

	callTimeout = 10 * time.Second
	waitTimeout = 5 * time.Second

	// appResizeTimeout bounds the wait for the Rex app to claim and size a
	// disowned block. The app answers in well under a second when it is
	// displaying the window; 8s only separates "slow" from "not displaying".
	appResizeTimeout = 8 * time.Second
)

// live is the run-wide state TestMain builds. Tests read it, never write it.
var live struct {
	c        *client.Client
	clientID string
	session  string
	window   string          // the window holding $REX_BLOCK: the one the user is looking at
	user     string          // $REX_BLOCK, the user's own pane: read, never written to
	apps     map[string]bool // client ids of the Rex apps attached to the session
	helper   string          // path of the built testdata/helper binary
	tmp      string          // mkdtemp dir under /tmp for helper reports
	hub      *hub
}

func TestMain(m *testing.M) { os.Exit(run(m)) }

// run is TestMain's body, split out so its defers (the leftover sweep,
// removing the temp dir) run before os.Exit, which skips them.
//
// The suite opens its layers over the window the user is already looking
// at, not over a window of its own: the Rex app only renders, and so only
// sizes, the window on screen, and a new window is not on screen until the
// user switches to it. It never closes, splits, rearranges or writes to the
// user's pane; it only reads the pane's size and, at the end, gives focus
// back to whatever block had it.
func run(m *testing.M) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "livetest: "+format+"\n", a...)
		return 1
	}
	session, user := os.Getenv("REX_SESSION"), os.Getenv("REX_BLOCK")
	if session == "" || user == "" {
		return fail("REX_SESSION and REX_BLOCK must be set: run from a shell inside a Rex pane, unsandboxed")
	}
	// The helper writes reports under /tmp, not t.TempDir(): the plan pins
	// reports there, and $TMPDIR on macOS is long and per-invocation.
	tmp, err := os.MkdirTemp("/tmp", "rexuikit-live-")
	if err != nil {
		return fail("mkdtemp: %v", err)
	}
	defer os.RemoveAll(tmp)

	helper := filepath.Join(tmp, "helper")
	build := exec.Command("go", "build", "-o", helper, "./testdata/helper")
	if out, err := build.CombinedOutput(); err != nil {
		return fail("build helper: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	c, err := client.Dial(ctx, client.Options{Name: labelPrefix, PingInterval: -1})
	if err != nil {
		return fail("dial Rex (is it running, and is this run unsandboxed?): %v", err)
	}
	defer c.Close()
	att, err := c.Attach(ctx, session)
	if err != nil {
		return fail("attach %s: %v", session, err)
	}
	h := newHub(c)

	sweep(ctx, c, session, "stale")
	window, focused := "", ""
	for _, w := range att.View.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				if b.BlockID == user {
					window, focused = w.WindowID, w.FocusedBlockID
				}
			}
		}
	}
	if window == "" {
		return fail("$REX_BLOCK %s is in no window of session %s", user, session)
	}

	apps, err := appClients(ctx, c, session)
	if err != nil {
		return fail("list clients: %v", err)
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "livetest: no Rex app client is attached to this session; app-sized tests will skip")
	}

	live.c, live.clientID, live.session = c, c.ClientID(), session
	live.window, live.user, live.apps = window, user, apps
	live.helper, live.tmp, live.hub = helper, tmp, h

	// Deferred after the client's Close, so they run first.
	defer func() {
		cctx, ccancel := context.WithTimeout(context.Background(), callTimeout)
		defer ccancel()
		sweep(cctx, c, session, "leftover")
		restoreFocus(cctx, c, session, window, focused)
	}()
	return m.Run()
}

// sweep closes every block labeled with labelPrefix: a crashed earlier run's
// leftovers at start, and anything a failed cleanup missed at the end. It
// closes through the client's block.close, which (unlike rex.call from a
// `rex do` script) does close detached blocks.
func sweep(ctx context.Context, c *client.Client, session, what string) {
	blocks, err := c.ListBlocks(ctx, session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "livetest: list_blocks for the %s sweep: %v\n", what, err)
		return
	}
	for _, b := range blocks {
		if !strings.HasPrefix(b.Label, labelPrefix) {
			continue
		}
		fmt.Fprintf(os.Stderr, "livetest: closing %s block %s (%s, %s)\n", what, b.BlockID, b.Label, b.Placement)
		if _, err := c.CloseBlock(ctx, session, b.BlockID); err != nil && !errors.Is(err, client.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "livetest: close %s block %s: %v\n", what, b.BlockID, err)
		}
	}
}

// restoreFocus gives focus back to the block that had it before the run.
// Closing a focused layer returns focus to the prior pane on its own, but
// the prior pane may have been another test layer, so this makes sure.
func restoreFocus(ctx context.Context, c *client.Client, session, window, focused string) {
	if focused == "" {
		return
	}
	v, err := c.View(ctx, session)
	if err != nil {
		return
	}
	for _, w := range v.Windows {
		if w.WindowID == window && w.FocusedBlockID != focused {
			if _, err := c.FocusBlock(ctx, session, focused); err != nil {
				fmt.Fprintf(os.Stderr, "livetest: restore focus to %s: %v\n", focused, err)
			}
		}
	}
}

func appClients(ctx context.Context, c *client.Client, session string) (map[string]bool, error) {
	var r struct {
		Clients []struct {
			ClientID string `json:"client_id"`
			Info     struct {
				Kind string `json:"kind"`
			} `json:"info"`
		} `json:"clients"`
	}
	if err := c.Call(ctx, "session.list_clients", map[string]string{"session_id": session}, &r); err != nil {
		return nil, err
	}
	apps := map[string]bool{}
	for _, cl := range r.Clients {
		if cl.Info.Kind == "app" {
			apps[cl.ClientID] = true
		}
	}
	return apps, nil
}

// hub fans the connection's session events out to per-test subscribers.
// The client fails the whole connection when its event buffer fills, so one
// goroutine drains it for the run; a subscriber that falls behind loses
// events rather than stalling that drain.
type hub struct {
	mu   sync.Mutex
	subs map[chan client.SessionEvent]struct{}
}

func newHub(c *client.Client) *hub {
	h := &hub{subs: map[chan client.SessionEvent]struct{}{}}
	go func() {
		for ev := range c.Events() {
			se, ok, err := ev.Session()
			if !ok || err != nil {
				continue
			}
			h.mu.Lock()
			for ch := range h.subs {
				select {
				case ch <- se:
				default:
				}
			}
			h.mu.Unlock()
		}
	}()
	return h
}

func (h *hub) subscribe(t *testing.T) <-chan client.SessionEvent {
	ch := make(chan client.SessionEvent, 1024)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	t.Cleanup(func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	})
	return ch
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

func termLayout(label string, argv []string, size *client.Size) client.Layout {
	return client.BlockLayout(client.TerminalFlavor, label, client.TerminalOptions{
		Shell: "none", Command: argv, Size: size,
	})
}

var reportSeq atomic.Int64

// helperArgv returns the argv that runs the helper and the report file it
// will write.
func helperArgv(t *testing.T) ([]string, string) {
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	out := filepath.Join(live.tmp, fmt.Sprintf("%s-%d.json", name, reportSeq.Add(1)))
	return []string{live.helper, "-out", out}, out
}

// helperReport mirrors testdata/helper's Report.
type helperReport struct {
	PID      int    `json:"pid"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Input    string `json:"input"`
	FocusIn  int    `json:"focus_in"`
	FocusOut int    `json:"focus_out"`
}

func readReport(path string) (helperReport, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return helperReport{}, false
	}
	var r helperReport
	if json.Unmarshal(b, &r) != nil {
		return helperReport{}, false
	}
	return r, true
}

// waitReport polls the helper's report until cond holds and fails the test
// with the last report seen when it never does.
func waitReport(t *testing.T, path, what string, cond func(helperReport) bool) helperReport {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var last helperReport
	for time.Now().Before(deadline) {
		if r, ok := readReport(path); ok {
			last = r
			if cond(r) {
				return r
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("helper never reported %s within %s; last report %+v", what, waitTimeout, last)
	return last
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", waitTimeout, what)
}

// newLayer opens a floating layer over the user's window and closes it at
// the end of the test. A layer whose program already exited is gone, so the
// cleanup ignores not_found.
func newLayer(t *testing.T, bounds client.Bounds, layout client.Layout, focus bool) client.NewLayerResult {
	t.Helper()
	l, err := live.c.NewLayer(ctxT(t), client.NewLayerArgs{
		SessionID: live.session, WindowID: live.window, Bounds: bounds, Layout: layout, Focus: focus,
	})
	if err != nil {
		t.Fatalf("new_layer %v: %v", bounds, err)
	}
	t.Cleanup(func() {
		closeQuietly(t, "close_layer", func(ctx context.Context) error {
			_, err := live.c.CloseLayer(ctx, live.session, l.LayerID)
			return err
		})
	})
	return l
}

// newDetached creates a detached terminal block and closes it at the end of
// the test, wherever it ended up.
func newDetached(t *testing.T, label string, argv []string, size *client.Size) string {
	t.Helper()
	id, err := live.c.NewBlock(ctxT(t), client.NewBlockArgs{
		SessionID: live.session,
		Flavor:    client.TerminalFlavor,
		Label:     label,
		Options:   client.TerminalOptions{Shell: "none", Command: argv, Size: size},
	})
	if err != nil {
		t.Fatalf("new_block: %v", err)
	}
	t.Cleanup(func() {
		closeQuietly(t, "block.close", func(ctx context.Context) error {
			_, err := live.c.CloseBlock(ctx, live.session, id)
			return err
		})
	})
	return id
}

func closeQuietly(t *testing.T, what string, f func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := f(ctx); err != nil && !errors.Is(err, client.ErrNotFound) {
		t.Errorf("cleanup %s: %v", what, err)
	}
}

func termSize(t *testing.T, block string) client.TerminalSize {
	t.Helper()
	s, err := live.c.TerminalSize(ctxT(t), live.session, block)
	if err != nil {
		t.Fatalf("terminal.size %s: %v", block, err)
	}
	return s
}

func findBlock(t *testing.T, block string) (client.BlockInfo, bool) {
	t.Helper()
	blocks, err := live.c.ListBlocks(ctxT(t), live.session)
	if err != nil {
		t.Fatalf("list_blocks: %v", err)
	}
	for _, b := range blocks {
		if b.BlockID == block {
			return b, true
		}
	}
	return client.BlockInfo{}, false
}

func userWindow(t *testing.T) client.Window {
	t.Helper()
	v, err := live.c.View(ctxT(t), live.session)
	if err != nil {
		t.Fatalf("session.view: %v", err)
	}
	for _, w := range v.Windows {
		if w.WindowID == live.window {
			return w
		}
	}
	t.Fatalf("window %s is not in session.view", live.window)
	return client.Window{}
}

func layerByID(w client.Window, id string) (client.Layer, bool) {
	for _, l := range w.Layers {
		if l.LayerID == id {
			return l, true
		}
	}
	return client.Layer{}, false
}

// sinkLayer opens a small idle layer for a focus test to move focus to, so
// focus-out never lands on (and sends a report to) the user's pane.
func sinkLayer(t *testing.T) string {
	t.Helper()
	l := newLayer(t, client.Bounds{0.7, 0.75, 0.25, 0.2},
		termLayout("rexuikit-livetest-sink", []string{"/bin/sleep", "600"}, nil), false)
	return l.BlockIDs[0]
}

// requireApp skips a test that needs the Rex app to size a block when no
// app client is attached at all: there is nothing to bring to the front.
func requireApp(t *testing.T) {
	t.Helper()
	if len(live.apps) == 0 {
		t.Skip("no Rex app client is attached to $REX_SESSION; this behavior needs the app to size blocks it displays (open the session in the Rex app and rerun)")
	}
}

// disown hands a block the suite created to nobody, so the Rex app can
// claim it. The creating connection owns a block, and while a non-app
// client owns it the app's requested size is not applied.
func disown(t *testing.T, block string) {
	t.Helper()
	err := live.c.Call(ctxT(t), "session.disown_block", map[string]any{
		"session_id": live.session, "block_ids": []string{block},
	}, nil)
	if err != nil {
		t.Fatalf("disown_block %s: %v", block, err)
	}
}

// appSize disowns a block and waits for the Rex app to claim and size it.
// "Sized" means an app client owns the block and its size has held for
// 300ms, since the app may resize twice while its layout settles. A block
// the app never claims fails with NO_APP_RESIZE and the observed owner,
// desired sizes and size; the assertion is never relaxed to pass.
func appSize(t *testing.T, block string) (cols, rows int) {
	t.Helper()
	requireApp(t)
	disown(t, block)
	deadline := time.Now().Add(appResizeTimeout)
	var stable time.Time
	var last client.TerminalSize
	for time.Now().Before(deadline) {
		s := termSize(t, block)
		switch {
		case !live.apps[s.Owner]:
			stable = time.Time{}
		case stable.IsZero() || s.Columns != last.Columns || s.Rows != last.Rows:
			stable = time.Now()
		case time.Since(stable) >= 300*time.Millisecond:
			return s.Columns, s.Rows
		}
		last = s
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("NO_APP_RESIZE: block %s never got an app owner within %s: owner %q, desired %+v, size %dx%d. The Rex app is attached but is not rendering window %s.",
		block, appResizeTimeout, last.Owner, last.Desired, last.Columns, last.Rows, live.window)
	return 0, 0
}

// windowCells is the size of the window the layers open over: the size of
// its lone tiled block, the user's pane, which the app already owns and
// sizes, plus its cell size in pixels. It is only read. rex-behaviors.md's
// chrome model is stated for an unsplit window, so a split one fails here
// rather than mis-measuring.
func windowCells(t *testing.T) client.TerminalSize {
	t.Helper()
	requireApp(t)
	for _, l := range userWindow(t).Layers {
		if l.Kind == "tiled" && (len(l.Blocks) != 1 || l.Blocks[0].BlockID != live.user) {
			t.Fatalf("window %s's tiled layer holds %d blocks; the chrome model needs a lone tiled block (unsplit window)", live.window, len(l.Blocks))
		}
	}
	s := termSize(t, live.user)
	if !live.apps[s.Owner] {
		t.Fatalf("NO_APP_RESIZE: the user's pane %s is owned by %q, not the Rex app; desired %+v, size %dx%d", live.user, s.Owner, s.Desired, s.Columns, s.Rows)
	}
	if s.CellWidthPx <= 0 || s.CellHeightPx <= 0 {
		t.Fatalf("terminal.size of %s has no cell px size (%dx%d); the chrome model needs it", live.user, s.CellWidthPx, s.CellHeightPx)
	}
	return s
}

// chromeRule is one axis of rex-behaviors.md's `## Layer chrome` model. The
// app lays a layer out in pixels and rounds each edge on its own, so the
// usable size depends on the layer's position, not only its fraction:
//
//	P      = window_cells * cell_px + framePx
//	edge   = edgeRounding(P * f)
//	usable = sizeRounding((edge(pos+frac) - edge(pos) - borderPx) / cell_px)
type chromeRule struct {
	framePx, borderPx  int
	edgeRound, sizeRnd string
}

func roundBy(mode string, v float64) float64 {
	switch mode {
	case "floor":
		return math.Floor(v)
	case "round":
		return math.Floor(v + 0.5)
	default:
		return math.Ceil(v)
	}
}

func (r chromeRule) usable(cells, cellPx int, pos, frac float64) int {
	p := float64(cells*cellPx + r.framePx)
	span := roundBy(r.edgeRound, p*(pos+frac)) - roundBy(r.edgeRound, p*pos)
	return max(1, int(roundBy(r.sizeRnd, (span-float64(r.borderPx))/float64(cellPx))))
}

// predictChrome is the usable size rex-behaviors.md predicts for a layer at
// bounds b over window w.
func predictChrome(cols, rows chromeRule, w client.TerminalSize, b client.Bounds) (int, int) {
	return cols.usable(w.Columns, w.CellWidthPx, b[0], b[2]), rows.usable(w.Rows, w.CellHeightPx, b[1], b[3])
}

var chromeRow = regexp.MustCompile("(?m)^\\|\\s*`?(FramePxCols|FramePxRows|BorderPxCols|BorderPxRows|EdgeRounding|SizeRounding)`?\\s*\\|\\s*(\\w+)\\s*\\|")

// loadChrome parses the chrome constants from the table in rex-behaviors.md
// `## Layer chrome`, never from rexuikit/surface: the document is the single
// source both surface and the generated Lua copy, so the test must catch
// either drifting from Rex.
//
//	| `FramePxCols`     | 52    |
//	| `BorderPxRows`    | 92    |
//	| `EdgeRounding`    | round |
func loadChrome(t *testing.T) (cols, rows chromeRule) {
	t.Helper()
	b, err := os.ReadFile(behaviorsMD)
	if err != nil {
		t.Fatalf("read %s: %v", behaviorsMD, err)
	}
	doc := string(b)
	i := strings.Index(doc, "\n## Layer chrome")
	if i < 0 {
		t.Fatalf("%s has no `## Layer chrome` section", behaviorsMD)
	}
	sec := doc[i+1:]
	if j := strings.Index(sec[1:], "\n## "); j >= 0 {
		sec = sec[:j+1]
	}
	v := map[string]string{}
	for _, m := range chromeRow.FindAllStringSubmatch(sec, -1) {
		if _, dup := v[m[1]]; !dup {
			v[m[1]] = m[2]
		}
	}
	num := func(k string) int {
		n, err := strconv.Atoi(v[k])
		if err != nil {
			t.Fatalf("%s `## Layer chrome`: %s = %q, want an integer", behaviorsMD, k, v[k])
		}
		return n
	}
	mode := func(k string) string {
		switch v[k] {
		case "floor", "round", "ceil":
			return v[k]
		}
		t.Fatalf("%s `## Layer chrome`: %s = %q, want floor, round or ceil", behaviorsMD, k, v[k])
		return ""
	}
	edge, size := mode("EdgeRounding"), mode("SizeRounding")
	cols = chromeRule{framePx: num("FramePxCols"), borderPx: num("BorderPxCols"), edgeRound: edge, sizeRnd: size}
	rows = chromeRule{framePx: num("FramePxRows"), borderPx: num("BorderPxRows"), edgeRound: edge, sizeRnd: size}
	return cols, rows
}

// TestNegotiatedRevision pins the API revision rexuikit/client speaks to the
// one the docs snapshot was taken at. A server whose revision moved past
// the snapshot fails here first, which is the cue to rerun
// scripts/rex-api-snapshot.sh and re-read the changed shapes.
func TestNegotiatedRevision(t *testing.T) {
	w := live.c.Welcome()
	if w.Protocol != client.Subprotocol {
		t.Errorf("welcome protocol = %q, want %q", w.Protocol, client.Subprotocol)
	}
	if got := live.c.APIRevision(); got != client.APIRevision {
		t.Errorf("negotiated api_revision = %d, want %d", got, client.APIRevision)
	}
	v, err := live.c.ServerVersion(ctxT(t))
	if err != nil {
		t.Fatalf("server.version: %v", err)
	}
	b, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("read %s: %v", versionFile, err)
	}
	snap := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if k, val, ok := strings.Cut(line, "="); ok {
			snap[k] = val
		}
	}
	if want := strconv.Itoa(v.APIRevision); snap["api_revision"] != want {
		t.Errorf("server api_revision = %d, docs snapshot has %q: rerun scripts/rex-api-snapshot.sh", v.APIRevision, snap["api_revision"])
	}
	if snap["rex_version"] != v.RexVersion {
		t.Logf("server is %s, snapshot taken at %s (same api_revision, so shapes match)", v.RexVersion, snap["rex_version"])
	}
}

// TestLayerDefaultSize80x24: a layer opened without `size` starts at 80x24,
// and its creator owns it. While the creator owns it without asking for a
// size, nothing resizes it, which is why rexuikit always passes `size`.
func TestLayerDefaultSize80x24(t *testing.T) {
	argv, out := helperArgv(t)
	l := newLayer(t, client.Bounds{0.55, 0.1, 0.4, 0.4}, termLayout("rexuikit-livetest-default", argv, nil), false)
	// Sampled twice: at once, and after the app (which is rendering this
	// window) has had time to request a size it would apply to a block it
	// owned. The creator still owns it, so it must not have moved.
	for _, wait := range []time.Duration{0, 800 * time.Millisecond} {
		time.Sleep(wait)
		s := termSize(t, l.BlockIDs[0])
		if s.Columns != defaultCols || s.Rows != defaultRows {
			t.Errorf("after %s terminal.size = %dx%d, want %dx%d", wait, s.Columns, s.Rows, defaultCols, defaultRows)
		}
		if s.Owner != live.clientID {
			t.Errorf("after %s owner = %q, want the creating client %q", wait, s.Owner, live.clientID)
		}
	}
	r := waitReport(t, out, "a size", func(r helperReport) bool { return r.Cols > 0 })
	if r.Cols != defaultCols || r.Rows != defaultRows {
		t.Errorf("program saw %dx%d, want %dx%d", r.Cols, r.Rows, defaultCols, defaultRows)
	}
}

// TestLayerSizeOptionHonored: the terminal option `size` sets the size the
// program sees from its first read, which is what lets a modal paint its
// first frame at the right size.
func TestLayerSizeOptionHonored(t *testing.T) {
	argv, out := helperArgv(t)
	want := client.Size{Columns: 57, Rows: 19}
	l := newLayer(t, client.Bounds{0.55, 0.1, 0.4, 0.4}, termLayout("rexuikit-livetest-sized", argv, &want), false)
	if s := termSize(t, l.BlockIDs[0]); s.Columns != want.Columns || s.Rows != want.Rows {
		t.Errorf("terminal.size = %dx%d, want %dx%d", s.Columns, s.Rows, want.Columns, want.Rows)
	}
	r := waitReport(t, out, "a size", func(r helperReport) bool { return r.Cols > 0 })
	if r.Cols != want.Columns || r.Rows != want.Rows {
		t.Errorf("program's first size = %dx%d, want %dx%d", r.Cols, r.Rows, want.Columns, want.Rows)
	}
}

// TestLayerChromeDelta re-measures Rex's floating-layer chrome (border and
// title) at several bounds and asserts every sample matches the model in
// rex-behaviors.md. The first three rows share one size at three x values:
// the size depends on position (57 columns at x=0.45, 58 at 0.55 and 0.60
// over a 149-column window), which a position-free rule cannot reproduce.
// The 0.5055 row sits on a cell boundary in both axes. The last rows are
// load-bearing for the rows constants: the centred 46x30 layer W6.T2 opened
// (the app gives 29 rows; the old 1862 px / 44 px model said 30) and two
// row spans that only the 1866 px / 46 px model gets right among the fits
// the earlier samples left.
func TestLayerChromeDelta(t *testing.T) {
	ruleC, ruleR := loadChrome(t)
	w := windowCells(t)
	t.Logf("window %dx%d cells, cell %dx%d px; model cols: P=W*%d+%d edges %s, border %dpx, %s; rows: P=H*%d+%d edges %s, border %dpx, %s",
		w.Columns, w.Rows, w.CellWidthPx, w.CellHeightPx,
		w.CellWidthPx, ruleC.framePx, ruleC.edgeRound, ruleC.borderPx, ruleC.sizeRnd,
		w.CellHeightPx, ruleR.framePx, ruleR.edgeRound, ruleR.borderPx, ruleR.sizeRnd)
	for _, b := range []client.Bounds{
		{0.55, 0.30, 0.40, 0.30},
		{0.45, 0.25, 0.40, 0.30},
		{0.60, 0.05, 0.40, 0.30},
		{0.50, 0.05, 0.45, 0.45},
		{0.60, 0.15, 0.33, 0.45},
		{0.62, 0.20, 0.30, 0.70},
		{0.60, 0.33, 0.34, 0.34},
		{0.5055, 0.5055, 0.30, 0.30},
		{0.3392857142857143, 0.3754027926960258, 0.32142857142857145, 0.2491944146079484},
		{0.10, 0.000, 0.40, 0.1369},
		{0.10, 0.112, 0.40, 0.2869},
	} {
		t.Run(fmt.Sprintf("%.4gx%.4g@%.4g,%.4g", b[2], b[3], b[0], b[1]), func(t *testing.T) {
			l := newLayer(t, b, termLayout("rexuikit-livetest-chrome", []string{"/bin/sleep", "600"}, nil), false)
			cols, rows := appSize(t, l.BlockIDs[0])
			wantC, wantR := predictChrome(ruleC, ruleR, w, b)
			t.Logf("chrome sample bounds=%v usable=%dx%d predicted=%dx%d delta_cols=%.2f delta_rows=%.2f (W*w-usable)",
				b, cols, rows, wantC, wantR, float64(w.Columns)*b[2]-float64(cols), float64(w.Rows)*b[3]-float64(rows))
			if cols != wantC || rows != wantR {
				t.Errorf("usable = %dx%d, rex-behaviors.md model predicts %dx%d", cols, rows, wantC, wantR)
			}
		})
	}
}

// TestLayerClosesOnProgramExit: when a layer's program exits, Rex closes
// the block and removes the layer, so a quick-action form only has to exit.
func TestLayerClosesOnProgramExit(t *testing.T) {
	events := live.hub.subscribe(t)
	l := newLayer(t, client.Bounds{0.55, 0.1, 0.4, 0.4},
		termLayout("rexuikit-livetest-exit", []string{"/bin/sh", "-c", "sleep 0.3"}, nil), false)
	block := l.BlockIDs[0]
	eventually(t, "the layer to leave session.view", func() bool {
		_, ok := layerByID(userWindow(t), l.LayerID)
		return !ok
	})
	if _, ok := findBlock(t, block); ok {
		t.Errorf("block %s still listed after its program exited", block)
	}
	if !sawEvent(events, "block_closed", block, time.Second) {
		t.Errorf("no block_closed event for %s", block)
	}
}

// sawEvent drains events until one of type typ naming block arrives.
func sawEvent(events <-chan client.SessionEvent, typ, block string, wait time.Duration) bool {
	timeout := time.After(wait)
	for {
		select {
		case se := <-events:
			if se.Type == typ && payloadBlock(se.Payload) == block {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

func payloadBlock(p json.RawMessage) string {
	var b struct {
		BlockID string `json:"block_id"`
	}
	_ = json.Unmarshal(p, &b)
	return b.BlockID
}

// TestFocusReportsForwarded: a program that enables DECSET 1004 receives a
// focus-in report when its block gains focus and a focus-out when it loses
// it. Rex has been seen to send each report twice; this asserts at least
// one, because surface/program dedupes by collapsing repeats rather than
// counting pairs, and that dedupe is unit-tested there.
func TestFocusReportsForwarded(t *testing.T) {
	argv, out := helperArgv(t)
	l := newLayer(t, client.Bounds{0.55, 0.1, 0.4, 0.4}, termLayout("rexuikit-livetest-focus", argv, nil), false)
	block := l.BlockIDs[0]
	sink := sinkLayer(t)
	waitReport(t, out, "ready", func(r helperReport) bool { return r.Cols > 0 })
	time.Sleep(150 * time.Millisecond) // let any report sent on DECSET 1004 itself land first
	before, _ := readReport(out)

	if _, err := live.c.FocusBlock(ctxT(t), live.session, block); err != nil {
		t.Fatalf("focus_block helper: %v", err)
	}
	in := waitReport(t, out, "a focus-in report", func(r helperReport) bool { return r.FocusIn > before.FocusIn })

	if _, err := live.c.FocusBlock(ctxT(t), live.session, sink); err != nil {
		t.Fatalf("focus_block sink: %v", err)
	}
	waitReport(t, out, "a focus-out report", func(r helperReport) bool { return r.FocusOut > in.FocusOut })
	time.Sleep(200 * time.Millisecond)
	final, _ := readReport(out)
	t.Logf("focus reports per change: in=%d out=%d (input %q)",
		in.FocusIn-before.FocusIn, final.FocusOut-before.FocusOut, final.Input)
}

// TestFocusChangeEmitsViewChanged: moving focus between blocks of one
// window emits session_view_changed and nothing else about the test's
// blocks: there is no dedicated focus event, so a surface tracking focus
// must re-read session.view on view changes.
func TestFocusChangeEmitsViewChanged(t *testing.T) {
	l := newLayer(t, client.Bounds{0.55, 0.5, 0.4, 0.4},
		termLayout("rexuikit-livetest-viewchg", []string{"/bin/sleep", "600"}, nil), false)
	block := l.BlockIDs[0]
	sink := sinkLayer(t)
	if _, err := live.c.FocusBlock(ctxT(t), live.session, sink); err != nil {
		t.Fatalf("focus_block sink: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	events := live.hub.subscribe(t)

	rev, err := live.c.FocusBlock(ctxT(t), live.session, block)
	if err != nil {
		t.Fatalf("focus_block: %v", err)
	}
	ours := map[string]bool{block: true, sink: true}
	var viewChanged bool
	var others []string
	timeout := time.After(600 * time.Millisecond)
collect:
	for {
		select {
		case se := <-events:
			switch {
			case se.SessionID != live.session:
			case se.Type == "session_view_changed":
				var p struct {
					Revision int64 `json:"revision"`
				}
				_ = json.Unmarshal(se.Payload, &p)
				if p.Revision >= rev {
					viewChanged = true
				}
			case se.Type == "active_window_changed":
				others = append(others, se.Type)
			default:
				// Other blocks in the session (the user's own tabs) emit
				// their own events; only ones about the test's blocks, or
				// with no block at all, count against the behavior.
				if b := payloadBlock(se.Payload); b == "" || ours[b] {
					others = append(others, se.Type)
				}
			}
		case <-timeout:
			break collect
		}
	}
	if !viewChanged {
		t.Errorf("no session_view_changed with revision >= %d after focus_block", rev)
	}
	if len(others) > 0 {
		t.Errorf("focus change also emitted %v; want only session_view_changed", others)
	}
	if w := userWindow(t); w.FocusedBlockID != block {
		t.Errorf("focused block = %s, want %s", w.FocusedBlockID, block)
	}
}

// TestFloatBlockRejectsDetached: session.float_block refuses a detached
// block with not_found, which is why pre-warm placement goes through a
// placeholder layer and move_block instead (see TestPrewarmPlacement).
func TestFloatBlockRejectsDetached(t *testing.T) {
	block := newDetached(t, "rexuikit-livetest-float", []string{"/bin/sleep", "600"}, nil)
	err := live.c.Call(ctxT(t), "session.float_block", map[string]any{
		"session_id": live.session, "block_id": block, "bounds": client.Bounds{0.2, 0.2, 0.5, 0.5},
	}, nil)
	if err == nil {
		t.Fatal("float_block on a detached block succeeded; the placeholder dance may no longer be needed")
	}
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("float_block error = %v, want code not_found", err)
	}
}

// TestPrewarmPlacement walks the pre-warm dance rexuikit/surface and the
// generated Lua perform: a detached block, placed via new_layer(placeholder)
// + move_block + close(placeholder), ends as the sole block of a floating
// layer; list_blocks flips its placement from detached to placed;
// set_block_label relabels it; the program gets a focus-in report once it
// is focused; and the app sizes it to the layer's usable size predicted by
// rex-behaviors.md's chrome model.
func TestPrewarmPlacement(t *testing.T) {
	argv, out := helperArgv(t)
	warmLabel := "rexuikit-livetest-warm:new:test"
	warm := newDetached(t, warmLabel, argv, &client.Size{Columns: 60, Rows: 20})
	waitReport(t, out, "ready", func(r helperReport) bool { return r.Cols > 0 })

	info, ok := findBlock(t, warm)
	if !ok || info.Placement != client.Detached || info.Label != warmLabel {
		t.Fatalf("before placement list_blocks has %+v (found %v), want placement %q label %q", info, ok, client.Detached, warmLabel)
	}

	bounds := client.Bounds{0.5, 0.2, 0.45, 0.6}
	ph := newLayer(t, bounds, termLayout("rexuikit-livetest-placeholder", []string{"/bin/sleep", "600"}, nil), true)
	if _, err := live.c.MoveBlock(ctxT(t), client.MoveBlockArgs{
		SessionID: live.session, BlockID: warm, AnchorBlockID: ph.BlockIDs[0],
	}); err != nil {
		t.Fatalf("move_block warm beside placeholder: %v", err)
	}
	if _, err := live.c.CloseBlock(ctxT(t), live.session, ph.BlockIDs[0]); err != nil {
		t.Fatalf("close placeholder: %v", err)
	}

	var layer client.Layer
	eventually(t, "the warm block to be the layer's only block", func() bool {
		for _, l := range userWindow(t).Layers {
			if len(l.Blocks) == 1 && l.Blocks[0].BlockID == warm {
				layer = l
				return true
			}
		}
		return false
	})
	if layer.Kind == "tiled" {
		t.Errorf("warm block landed in the tiled layer, want a floating layer")
	}
	if layer.LayerID != ph.LayerID {
		t.Logf("layer id changed across the dance: %s -> %s", ph.LayerID, layer.LayerID)
	}

	info, _ = findBlock(t, warm)
	if info.Placement != client.Placed || info.WindowID != live.window {
		t.Errorf("after placement list_blocks has placement %q window %q, want %q %q", info.Placement, info.WindowID, client.Placed, live.window)
	}
	liveLabel := "rexuikit-livetest-live:new"
	if _, err := live.c.SetBlockLabel(ctxT(t), live.session, warm, liveLabel); err != nil {
		t.Fatalf("set_block_label on the placed block: %v", err)
	}
	if info, _ = findBlock(t, warm); info.Label != liveLabel {
		t.Errorf("label after set_block_label = %q, want %q", info.Label, liveLabel)
	}

	before, _ := readReport(out)
	if _, err := live.c.FocusBlock(ctxT(t), live.session, warm); err != nil {
		t.Fatalf("focus_block warm: %v", err)
	}
	waitReport(t, out, "a focus-in report after placement", func(r helperReport) bool { return r.FocusIn > before.FocusIn })

	if len(live.apps) == 0 {
		t.Skip("placement verified; final-size check skipped: no Rex app client attached to size the layer")
	}
	ruleC, ruleR := loadChrome(t)
	w := windowCells(t)
	cols, rows := appSize(t, warm)
	wantC, wantR := predictChrome(ruleC, ruleR, w, bounds)
	if cols != wantC || rows != wantR {
		t.Errorf("placed block size = %dx%d, layer usable size per rex-behaviors.md = %dx%d", cols, rows, wantC, wantR)
	}
	waitReport(t, out, fmt.Sprintf("size %dx%d", cols, rows), func(r helperReport) bool { return r.Cols == cols && r.Rows == rows })
}

// TestOrderedMethodsSerialize: requests of the ordered methods run one at a
// time in arrival order per connection. It is observed through client.action,
// whose result names the ordered request that ran just before it
// (ordered_after). rexuikit/client does not expose request ids, so this test
// opens its own raw connection and picks the ids itself, writing a burst of
// requests before reading any response.
func TestOrderedMethodsSerialize(t *testing.T) {
	argv, out := helperArgv(t)
	l := newLayer(t, client.Bounds{0.55, 0.5, 0.4, 0.4}, termLayout("rexuikit-livetest-ordered", argv, nil), false)
	waitReport(t, out, "ready", func(r helperReport) bool { return r.Cols > 0 })

	conn := rawDial(t)
	rawCall(t, conn, "attach", "session.attach", map[string]string{"session_id": live.session})

	const n = 8
	ctx := ctxT(t)
	var want strings.Builder
	for i := 1; i <= n; i++ {
		data := fmt.Sprintf("k%d;", i)
		want.WriteString(data)
		req, _ := json.Marshal(map[string]any{
			"type": "request", "id": fmt.Sprintf("ord-%d", i), "method": "client.action",
			"payload": map[string]any{
				"action": "com.superlogical.terminal.write", "session_id": live.session,
				"block_id": l.BlockIDs[0], "args": map[string]string{"data": data},
			},
		})
		if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
			t.Fatalf("write ord-%d: %v", i, err)
		}
	}
	after := map[string]string{}
	for len(after) < n {
		env := rawRead(t, conn)
		if env.Type != "response" || !strings.HasPrefix(env.ID, "ord-") {
			continue
		}
		if env.Error != nil {
			t.Fatalf("%s failed: %s", env.ID, env.Error)
		}
		var p struct {
			OrderedAfter string `json:"ordered_after"`
		}
		_ = json.Unmarshal(env.Payload, &p)
		after[env.ID] = p.OrderedAfter
	}
	if after["ord-2"] == "" {
		t.Skip("not observable: client.action results carry no ordered_after on this server")
	}
	for i := 2; i <= n; i++ {
		id, prev := fmt.Sprintf("ord-%d", i), fmt.Sprintf("ord-%d", i-1)
		if after[id] != prev {
			t.Errorf("%s ordered_after = %q, want %q", id, after[id], prev)
		}
	}
	waitReport(t, out, "every write, in order", func(r helperReport) bool { return strings.Contains(r.Input, want.String()) })
}

type rawEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
	Error   json.RawMessage `json:"error"`
}

func rawDial(t *testing.T) *websocket.Conn {
	t.Helper()
	addr, err := client.ServerAddr(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	sock, err := client.SocketPath(addr)
	if err != nil {
		t.Fatal(err)
	}
	var d net.Dialer
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	ctx := ctxT(t)
	conn, _, err := websocket.Dial(ctx, "ws://rex"+client.ControlPath, &websocket.DialOptions{
		HTTPClient: hc, Subprotocols: []string{client.Subprotocol},
	})
	if err != nil {
		t.Fatalf("raw dial: %v", err)
	}
	conn.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	hello, _ := json.Marshal(map[string]any{
		"type": "hello", "api_revision": client.APIRevision, "api_min_revision": client.MinAPIRevision,
		"client": map[string]string{"kind": "cli", "name": "rexuikit-livetest-raw"},
	})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("raw hello: %v", err)
	}
	if w := rawRead(t, conn); w.Type != "welcome" {
		t.Fatalf("raw handshake got %q, want welcome", w.Type)
	}
	return conn
}

func rawRead(t *testing.T, conn *websocket.Conn) rawEnvelope {
	t.Helper()
	_, b, err := conn.Read(ctxT(t))
	if err != nil {
		t.Fatalf("raw read: %v", err)
	}
	var env rawEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("raw decode %s: %v", b, err)
	}
	return env
}

func rawCall(t *testing.T, conn *websocket.Conn, id, method string, payload any) {
	t.Helper()
	req, _ := json.Marshal(map[string]any{"type": "request", "id": id, "method": method, "payload": payload})
	if err := conn.Write(ctxT(t), websocket.MessageText, req); err != nil {
		t.Fatalf("raw write %s: %v", method, err)
	}
	for {
		env := rawRead(t, conn)
		if env.Type == "response" && env.ID == id {
			if env.Error != nil {
				t.Fatalf("raw %s: %s", method, env.Error)
			}
			return
		}
	}
}

// TestListBlocksIncludesDetachedWithLabel: session.list_blocks lists a
// detached block with its label and placement "detached". The warm pool
// finds its blocks this way (label prefix AND placement detached).
func TestListBlocksIncludesDetachedWithLabel(t *testing.T) {
	label := "rexuikit-livetest-detached"
	block := newDetached(t, label, []string{"/bin/sleep", "600"}, nil)
	info, ok := findBlock(t, block)
	if !ok {
		t.Fatalf("detached block %s missing from list_blocks", block)
	}
	if info.Label != label || info.Placement != client.Detached || info.WindowID != "" {
		t.Errorf("list_blocks entry = %+v, want label %q, placement %q, no window", info, label, client.Detached)
	}
}

// TestDetachedBlockEnvNamesOwnBlock: Rex starts a block's program with
// REX_BLOCK set to that block's own id, also for a detached block made over
// the API by a client whose own REX_BLOCK names another block. A warm forge
// program relies on it to take only the placement context naming its own
// block; the rest of the server's environment is not relied on.
func TestDetachedBlockEnvNamesOwnBlock(t *testing.T) {
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	out := filepath.Join(live.tmp, fmt.Sprintf("%s-%d.env", name, reportSeq.Add(1)))
	block := newDetached(t, "rexuikit-livetest-env",
		[]string{"/bin/sh", "-c", `printf %s "$REX_BLOCK" > "$0"; exec /bin/sleep 600`, out}, nil)
	var got string
	eventually(t, "the detached program to write its REX_BLOCK", func() bool {
		b, err := os.ReadFile(out)
		got = string(b)
		return err == nil && got != ""
	})
	if got != block {
		t.Errorf("REX_BLOCK in the detached program = %q, want its own block %q", got, block)
	}
}

// TestDetachedBlockClosesOnProgramExit: a detached block whose program
// exits leaves list_blocks, as a placed one does. The warm pool's liveness
// check relies on it: a warm form that crashed disappears instead of being
// placed as a blank modal.
func TestDetachedBlockClosesOnProgramExit(t *testing.T) {
	events := live.hub.subscribe(t)
	block := newDetached(t, "rexuikit-livetest-detached-exit", []string{"/bin/sh", "-c", "sleep 0.3"}, nil)
	eventually(t, "the exited detached block to leave list_blocks", func() bool {
		_, ok := findBlock(t, block)
		return !ok
	})
	if !sawEvent(events, "block_closed", block, time.Second) {
		t.Errorf("no block_closed event for detached %s", block)
	}
}

// TestWarmBlockResizeBeforePlacement: terminal.resize applies to a detached
// block its creator owns, and the program sees the new size. A warm block
// can therefore be re-sized for the window it is about to be placed in.
func TestWarmBlockResizeBeforePlacement(t *testing.T) {
	argv, out := helperArgv(t)
	block := newDetached(t, "rexuikit-livetest-warm-resize", argv, &client.Size{Columns: 70, Rows: 20})
	waitReport(t, out, "70x20", func(r helperReport) bool { return r.Cols == 70 && r.Rows == 20 })
	want := client.Size{Columns: 61, Rows: 17}
	if err := live.c.BlockMethod(ctxT(t), live.session, block, "com.superlogical.terminal.resize", want, nil); err != nil {
		t.Fatalf("terminal.resize detached: %v", err)
	}
	if s := termSize(t, block); s.Columns != want.Columns || s.Rows != want.Rows {
		t.Errorf("terminal.size = %dx%d, want %dx%d", s.Columns, s.Rows, want.Columns, want.Rows)
	}
	waitReport(t, out, "61x17", func(r helperReport) bool { return r.Cols == want.Columns && r.Rows == want.Rows })
}

// TestLuaCanWriteFile: a `rex do` script can write a file with io.open and
// rename it with os.rename under the pre-warm directory, which is how the
// generated forge Lua hands the placement context to a warm program.
//
// `rex do` runs with the calling CLI's environment, not the Rex server's,
// so this cannot show what environment config Lua (key bindings) sees. That
// is why the generated Lua gets the pre-warm directory baked in at install
// time instead of reading HOME or XDG variables itself.
func TestLuaCanWriteFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// A directory under $HOME that the test owns, so it proves Rex can
	// write there and nothing about any application's state layout.
	dir := filepath.Join(home, ".local", "state", "rexuikit-livetest")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("rexuikit-livetest-%d.json", os.Getpid())
	final := filepath.Join(dir, name)
	t.Cleanup(func() {
		_ = os.Remove(final)
		_ = os.Remove(final + ".tmp")
	})
	// `rex do` pre-decodes a JSON-looking KEY=VALUE value into a Lua table,
	// so the body is a plain token that stays a string.
	body := "rexuikit-livetest-body"
	cmd := exec.Command("rex", "do", "testdata/luawrite.lua", "dir="+dir, "name="+name, "body="+body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("rex do luawrite.lua: %v\nstdout: %s\nstderr: %s", err, stdout, stderr.String())
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("renamed file missing: %v (stdout %q)", err, stdout)
	}
	if string(got) != body {
		t.Errorf("file body = %q, want %q", got, body)
	}
	if _, err := os.Stat(final + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file still present after os.rename (stat err %v)", err)
	}
}
