package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dphaener/rexuikit/client"
	"github.com/dphaener/rexuikit/client/clienttest"
)

const realSocket = "/Users/alice/Library/Application Support/rex/server.sock"

func dial(t *testing.T, srv *clienttest.Server, opts client.Options) *client.Client {
	t.Helper()
	opts.Addr = srv.URL
	opts.HTTPClient = srv.HTTPClient()
	if opts.PingInterval == 0 {
		opts.PingInterval = -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// waitFor polls cond in the test goroutine; fake-server goroutines never
// assert, they only record.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func requestCount(srv *clienttest.Server, method string) func() bool {
	return func() bool {
		for _, r := range srv.Requests() {
			if r.Method == method {
				return true
			}
		}
		return false
	}
}

func assertNoFakeErrs(t *testing.T, srv *clienttest.Server) {
	t.Helper()
	for _, err := range srv.Errs() {
		t.Errorf("fake server: %v", err)
	}
}

// TestSocketPath pins REX_SERVER decoding. Load-bearing rows: "real macOS
// path" (the app's socket lives under "Application Support", whose space
// arrives as %20; failing to decode it dials a path that does not exist) and
// "decodes once" (a literal % in a directory name must survive, so the path
// is unescaped exactly once, matching rex-api-snapshot.sh).
func TestSocketPath(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    string
		wantErr bool
	}{
		{"real macOS path", "unix:///Users/alice/Library/Application%20Support/rex/server.sock", realSocket, false},
		{"unencoded path", "unix:///tmp/rex.sock", "/tmp/rex.sock", false},
		{"decodes once", "unix:///tmp/a%2520b.sock", "/tmp/a%20b.sock", false},
		{"not unix", "http://localhost:1234", "", true},
		{"bad escape", "unix:///tmp/%zz", "", true},
		{"empty path", "unix://", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.SocketPath(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SocketPath(%q) err = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SocketPath(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

// TestServerAddr: "default socket" is load-bearing, because the default
// must survive the same decoding a REX_SERVER value goes through.
func TestServerAddr(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantPath string
		wantErr  bool
	}{
		{"REX_SERVER wins", map[string]string{"REX_SERVER": "unix:///tmp/x%20y.sock", "HOME": "/Users/nobody"}, "/tmp/x y.sock", false},
		{"default socket", map[string]string{"HOME": "/Users/alice"}, realSocket, false},
		{"nothing set", map[string]string{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := client.ServerAddr(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("ServerAddr err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, err := client.SocketPath(addr)
			if err != nil {
				t.Fatalf("SocketPath(%q): %v", addr, err)
			}
			if got != tt.wantPath {
				t.Errorf("path = %q, want %q (addr %q)", got, tt.wantPath, addr)
			}
		})
	}
}

func TestDialHandshakeSuccess(t *testing.T) {
	srv := clienttest.New(t)
	c := dial(t, srv, client.Options{Name: "forge", Version: "1.2.3"})

	if got := c.APIRevision(); got != 7 {
		t.Errorf("APIRevision = %d, want 7", got)
	}
	if got := c.ClientID(); got != "client:fake" {
		t.Errorf("ClientID = %q", got)
	}
	if got := c.ReconnectToken(); got != "token-fake" {
		t.Errorf("ReconnectToken = %q", got)
	}
	if got := c.Welcome().Limits.MaxMessageBytes; got != 1<<20 {
		t.Errorf("max_message_bytes = %d", got)
	}

	hellos := srv.Hellos()
	if len(hellos) != 1 {
		t.Fatalf("hellos = %d, want 1", len(hellos))
	}
	h := hellos[0]
	if h.APIRevision != client.APIRevision || h.APIMinRevision != client.MinAPIRevision {
		t.Errorf("hello revisions = %d..%d", h.APIMinRevision, h.APIRevision)
	}
	if h.ReconnectToken != "" {
		t.Errorf("first hello sent a reconnect token")
	}
	var about struct{ Kind, Name, Version string }
	if err := json.Unmarshal(h.Client, &about); err != nil {
		t.Fatal(err)
	}
	if about.Kind != "cli" || about.Name != "forge" || about.Version != "1.2.3" {
		t.Errorf("hello client = %+v", about)
	}
	if len(h.Raw) >= 512 {
		t.Errorf("hello is %d bytes, spec requires under 512", len(h.Raw))
	}
	assertNoFakeErrs(t, srv)
}

// TestDialHandshakeFailures: every row must fail Dial. Load-bearing:
// "no welcome" (a server that accepts but never answers must not hang the
// caller past HandshakeTimeout), "wrong protocol" (the spec's check before
// anything else is sent) and "closed 1008" (how a real server refuses a
// client with no common revision; the close status must reach the caller).
func TestDialHandshakeFailures(t *testing.T) {
	welcome := func(mut func(map[string]any)) clienttest.WelcomeFunc {
		return func(h clienttest.Hello) any {
			m := clienttest.DefaultWelcome(h).(map[string]any)
			mut(m)
			return m
		}
	}
	tests := []struct {
		name       string
		welcome    clienttest.WelcomeFunc
		wantStatus websocket.StatusCode
		wantSubstr string
	}{
		{"no welcome", func(clienttest.Hello) any { return nil }, -1, "no welcome"},
		{"wrong protocol", welcome(func(m map[string]any) { m["protocol"] = "rex.control.v2" }), -1, "protocol"},
		{"first message not a welcome", func(clienttest.Hello) any {
			return map[string]any{"type": "event", "seq": 1, "payload": map[string]any{"type": "x"}}
		}, -1, "want welcome"},
		{"revision below range", welcome(func(m map[string]any) { m["api_revision"] = 6 }), -1, "revision"},
		{"revision above range", welcome(func(m map[string]any) { m["api_revision"] = 8 }), -1, "revision"},
		{"closed 1008", func(clienttest.Hello) any {
			return clienttest.Close{Code: websocket.StatusPolicyViolation, Reason: "no common API revision: update the client"}
		}, websocket.StatusPolicyViolation, "1008"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := clienttest.New(t)
			srv.SetWelcome(tt.welcome)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			c, err := client.Dial(ctx, client.Options{
				Addr:             srv.URL,
				HTTPClient:       srv.HTTPClient(),
				HandshakeTimeout: 300 * time.Millisecond,
				PingInterval:     -1,
			})
			if err == nil {
				_ = c.Close()
				t.Fatal("Dial succeeded, want error")
			}
			if time.Since(start) > 3*time.Second {
				t.Errorf("Dial took %v; the handshake timeout did not bound it", time.Since(start))
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantSubstr)
			}
			if tt.wantStatus != -1 {
				if got := websocket.CloseStatus(err); got != tt.wantStatus {
					t.Errorf("close status = %d, want %d", got, tt.wantStatus)
				}
			}
		})
	}
}

func TestDialRejectsMissingSubprotocol(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil) // selects no subprotocol
		if err != nil {
			return
		}
		_, _, _ = ws.Read(r.Context())
		_ = ws.CloseNow()
	}))
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, client.Options{Addr: hs.URL, HTTPClient: hs.Client(), PingInterval: -1})
	if err == nil {
		_ = c.Close()
		t.Fatal("Dial succeeded against a server without rex.control.v1")
	}
	if !strings.Contains(err.Error(), "subprotocol") {
		t.Errorf("err = %v", err)
	}
}

func TestConcurrentCallsResolveOutOfOrder(t *testing.T) {
	srv := clienttest.New(t)
	release := make(chan struct{})
	srv.Handle("slow", func(ctx context.Context, _ clienttest.Request) (any, *clienttest.Error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return map[string]string{"who": "slow"}, nil
	})
	srv.Handle("echo", func(_ context.Context, r clienttest.Request) (any, *clienttest.Error) {
		return json.RawMessage(r.Payload), nil
	})
	c := dial(t, srv, client.Options{})
	ctx := context.Background()

	slowDone := make(chan error, 1)
	var slowOut struct{ Who string }
	go func() { slowDone <- c.Call(ctx, "slow", nil, &slowOut) }()
	waitFor(t, "slow request", requestCount(srv, "slow"))

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{ N int }
			if err := c.Call(ctx, "echo", map[string]int{"n": i}, &out); err != nil {
				errs[i] = err
				return
			}
			if out.N != i {
				errs[i] = fmt.Errorf("call %d got answer %d", i, out.N)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	select {
	case err := <-slowDone:
		t.Fatalf("slow call returned before release: %v", err)
	default:
	}

	close(release)
	if err := <-slowDone; err != nil {
		t.Fatalf("slow call: %v", err)
	}
	if slowOut.Who != "slow" {
		t.Errorf("slow call got %+v", slowOut)
	}

	seen := map[string]bool{}
	for _, r := range srv.Requests() {
		if seen[r.ID] {
			t.Errorf("request id %q reused", r.ID)
		}
		seen[r.ID] = true
	}
	assertNoFakeErrs(t, srv)
}

// TestErrorCodeMapping: load-bearing rows are "session_not_found is not
// not_found" (both are 404; callers creating a session on
// session_not_found must not also do it for a missing block) and "unknown
// code falls back to status" (the spec's rule for codes added later).
func TestErrorCodeMapping(t *testing.T) {
	all := map[string]error{
		"bad_request":       client.ErrBadRequest,
		"not_found":         client.ErrNotFound,
		"method_not_found":  client.ErrMethodNotFound,
		"session_not_found": client.ErrSessionNotFound,
		"conflict":          client.ErrConflict,
		"timeout":           client.ErrTimeout,
		"too_large":         client.ErrTooLarge,
		"revision_too_low":  client.ErrRevisionTooLow,
	}
	tests := []struct {
		name   string
		code   string
		status int
		want   string // the only sentinel in all that must match
	}{
		{"bad_request", "bad_request", 400, "bad_request"},
		{"not_found", "not_found", 404, "not_found"},
		{"method_not_found is not not_found", "method_not_found", 404, "method_not_found"},
		{"session_not_found is not not_found", "session_not_found", 404, "session_not_found"},
		{"conflict", "conflict", 409, "conflict"},
		{"timeout", "timeout", 504, "timeout"},
		{"revision_too_low is not bad_request", "revision_too_low", 400, "revision_too_low"},
		{"unknown code falls back to status", "layer_gone", 404, "not_found"},
		{"unknown code unknown status matches nothing", "weird", 418, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := clienttest.New(t)
			srv.Fail("m", tt.code, tt.status, "words that may change")
			c := dial(t, srv, client.Options{})
			err := c.Call(context.Background(), "m", nil, nil)
			var re *client.Error
			if !errors.As(err, &re) {
				t.Fatalf("err = %v (%T), want *client.Error", err, err)
			}
			if re.Code != tt.code || re.Status != tt.status || re.Method != "m" {
				t.Errorf("error = %+v", re)
			}
			for code, sentinel := range all {
				if got := errors.Is(err, sentinel); got != (code == tt.want) {
					t.Errorf("errors.Is(%s) = %v", code, got)
				}
			}
		})
	}

	t.Run("unregistered method", func(t *testing.T) {
		srv := clienttest.New(t)
		c := dial(t, srv, client.Options{})
		if err := c.Call(context.Background(), "nope", nil, nil); !errors.Is(err, client.ErrMethodNotFound) {
			t.Errorf("err = %v, want method_not_found", err)
		}
	})
}

func TestEventsDeliveredInSeqOrder(t *testing.T) {
	srv := clienttest.New(t)
	c := dial(t, srv, client.Options{})

	emits := []any{
		clienttest.EventOf("session_created", map[string]any{"session": map[string]string{"session_id": "session:1"}}),
		clienttest.SessionEventOf("session:1", "block_created", map[string]string{"block_id": "block:a"}),
		clienttest.BlockEventOf("session:1", "block:a", "com.superlogical.terminal", "bell", nil),
		clienttest.EventOf("brand_new_event", map[string]int{"x": 1}),
		clienttest.SessionEventOf("session:1", "session_view_changed", map[string]int{"revision": 9}),
	}
	for _, e := range emits {
		if _, err := srv.Emit(e); err != nil {
			t.Fatal(err)
		}
	}

	var got []client.Event
	timeout := time.After(5 * time.Second)
	for len(got) < len(emits) {
		select {
		case e := <-c.Events():
			got = append(got, e)
		case <-timeout:
			t.Fatalf("got %d of %d events", len(got), len(emits))
		}
	}
	wantTypes := []string{"session_created", "session_event", "session_event", "brand_new_event", "session_event"}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d", i, e.Seq)
		}
		if e.Type != wantTypes[i] {
			t.Errorf("event %d type = %q, want %q", i, e.Type, wantTypes[i])
		}
	}

	if _, ok, _ := got[0].Session(); ok {
		t.Error("top-level event unwrapped as a session event")
	}
	se, ok, err := got[1].Session()
	if !ok || err != nil || se.SessionID != "session:1" || se.Type != "block_created" {
		t.Errorf("Session() = %+v %v %v", se, ok, err)
	}
	if _, ok, _ := got[1].Block(); ok {
		t.Error("block_created unwrapped as a block_event")
	}
	be, ok, err := got[2].Block()
	if !ok || err != nil || be.BlockID != "block:a" || be.Name != "bell" || be.CreatorName != "com.superlogical.terminal" || be.SessionID != "session:1" {
		t.Errorf("Block() = %+v %v %v", be, ok, err)
	}
}

func TestEventSeqGapFailsConnection(t *testing.T) {
	srv := clienttest.New(t)
	c := dial(t, srv, client.Options{})
	if err := srv.Send(map[string]any{"type": "event", "seq": 2, "payload": clienttest.EventOf("x", nil)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection survived a seq gap")
	}
	if !errors.Is(c.Err(), client.ErrEventGap) {
		t.Errorf("Err = %v, want ErrEventGap", c.Err())
	}
	if _, open := <-c.Events(); open {
		t.Error("Events still open after failure")
	}
}

func TestEventBufferOverflowSurfacesError(t *testing.T) {
	srv := clienttest.New(t)
	srv.Handle("block", func(ctx context.Context, _ clienttest.Request) (any, *clienttest.Error) {
		<-ctx.Done()
		return nil, nil
	})
	c := dial(t, srv, client.Options{EventBuffer: 2})

	pending := make(chan error, 1)
	go func() { pending <- c.Call(context.Background(), "block", nil, nil) }()
	waitFor(t, "blocked request", requestCount(srv, "block"))

	for i := 0; i < 3; i++ {
		if _, err := srv.Emit(clienttest.EventOf("tick", i)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("overflow did not end the connection")
	}
	if !errors.Is(c.Err(), client.ErrEventOverflow) {
		t.Errorf("Err = %v, want ErrEventOverflow", c.Err())
	}
	if err := <-pending; !errors.Is(err, client.ErrEventOverflow) {
		t.Errorf("pending call err = %v, want ErrEventOverflow", err)
	}
	// The two buffered events are still readable, then the channel closes:
	// nothing was dropped silently.
	var n int
	for range c.Events() {
		n++
	}
	if n != 2 {
		t.Errorf("drained %d buffered events, want 2", n)
	}
}

func TestDisableEvents(t *testing.T) {
	srv := clienttest.New(t)
	srv.Reply("ping", map[string]bool{"ok": true})
	c := dial(t, srv, client.Options{DisableEvents: true, EventBuffer: 1})
	if c.Events() != nil {
		t.Error("Events() not nil with DisableEvents")
	}
	for i := 0; i < 10; i++ {
		if _, err := srv.Emit(clienttest.EventOf("tick", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Call(context.Background(), "ping", nil, nil); err != nil {
		t.Errorf("call after undrained events: %v", err)
	}
}

func TestCloseUnblocksPendingCalls(t *testing.T) {
	srv := clienttest.New(t)
	srv.Handle("hang", func(ctx context.Context, _ clienttest.Request) (any, *clienttest.Error) {
		<-ctx.Done()
		return nil, nil
	})
	c := dial(t, srv, client.Options{})

	const n = 3
	results := make(chan error, n)
	for range n {
		go func() { results <- c.Call(context.Background(), "hang", nil, nil) }()
	}
	waitFor(t, "hanging requests", func() bool { return len(srv.Requests()) == n })

	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	for range n {
		select {
		case err := <-results:
			if !errors.Is(err, client.ErrClosed) {
				t.Errorf("pending call err = %v, want ErrClosed", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close left a call pending")
		}
	}
	if err := c.Call(context.Background(), "hang", nil, nil); !errors.Is(err, client.ErrClosed) {
		t.Errorf("call after Close = %v, want ErrClosed", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, open := <-c.Events(); open {
		t.Error("Events open after Close")
	}
}

func TestServerDropFailsPendingCalls(t *testing.T) {
	srv := clienttest.New(t)
	srv.Handle("hang", func(ctx context.Context, _ clienttest.Request) (any, *clienttest.Error) {
		<-ctx.Done()
		return nil, nil
	})
	c := dial(t, srv, client.Options{})
	result := make(chan error, 1)
	go func() { result <- c.Call(context.Background(), "hang", nil, nil) }()
	waitFor(t, "hanging request", requestCount(srv, "hang"))

	if err := srv.Drop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || errors.Is(err, client.ErrClosed) {
			t.Errorf("err = %v, want a connection-lost error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dropped connection left a call pending")
	}
}

// TestCancelledCallKeepsConnection guards the write-context decision: a
// caller's cancelled context must abandon only its own wait, not close the
// connection under everyone else.
func TestCancelledCallKeepsConnection(t *testing.T) {
	srv := clienttest.New(t)
	srv.Handle("hang", func(ctx context.Context, _ clienttest.Request) (any, *clienttest.Error) {
		<-ctx.Done()
		return nil, nil
	})
	srv.Reply("ok", map[string]int{"v": 1})
	c := dial(t, srv, client.Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "hang", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
	var out struct{ V int }
	if err := c.Call(context.Background(), "ok", nil, &out); err != nil || out.V != 1 {
		t.Errorf("call after a cancelled one: %v %+v", err, out)
	}
}

func TestOversizedRequestRefusedLocally(t *testing.T) {
	srv := clienttest.New(t)
	srv.SetWelcome(func(h clienttest.Hello) any {
		m := clienttest.DefaultWelcome(h).(map[string]any)
		m["limits"] = map[string]any{"max_message_bytes": 65536}
		return m
	})
	srv.Reply("ok", map[string]int{})
	c := dial(t, srv, client.Options{})

	err := c.Call(context.Background(), "big", map[string]string{"data": strings.Repeat("x", 70000)}, nil)
	if err == nil || !strings.Contains(err.Error(), "at most 65536") {
		t.Errorf("err = %v, want a local size refusal", err)
	}
	if requestCount(srv, "big")() {
		t.Error("oversized request reached the server")
	}
	if err := c.Call(context.Background(), "ok", nil, nil); err != nil {
		t.Errorf("connection unusable after refusal: %v", err)
	}
}

// TestMethodPayloads pins the wire shape of each typed helper. Load-bearing
// rows: new_layer (bounds must be a JSON array, and focus omitted when false
// so the server default applies), new_window with Focus nil (the server
// default is true, so an unset Focus must be omitted, never sent as false),
// and terminal.size (block methods carry arguments under "args", never at
// the top level).
func TestMethodPayloads(t *testing.T) {
	f := false
	tests := []struct {
		name   string
		method string
		call   func(*client.Client) error
		want   string
	}{
		{"server.version", "server.version", func(c *client.Client) error { _, err := c.ServerVersion(context.Background()); return err }, `null`},
		{"session.list", "session.list", func(c *client.Client) error { _, err := c.ListSessions(context.Background()); return err }, `null`},
		{"session.detach", "session.detach", func(c *client.Client) error { return c.Detach(context.Background(), "s") }, `{"session_id":"s"}`},
		{"session.view", "session.view", func(c *client.Client) error { _, err := c.View(context.Background(), "s"); return err }, `{"session_id":"s"}`},
		{"new_window default focus", "session.new_window", func(c *client.Client) error {
			_, err := c.NewWindow(context.Background(), client.NewWindowArgs{SessionID: "s", Label: "w", Layout: client.BlockLayout(client.TerminalFlavor, "", nil)})
			return err
		}, `{"session_id":"s","window_label":"w","layout":{"block":{"flavor":"com.superlogical.terminal.shell"}}}`},
		{"new_window no focus", "session.new_window", func(c *client.Client) error {
			_, err := c.NewWindow(context.Background(), client.NewWindowArgs{SessionID: "s", Layout: client.BlockLayout(client.TerminalFlavor, "", nil), Focus: &f})
			return err
		}, `{"session_id":"s","layout":{"block":{"flavor":"com.superlogical.terminal.shell"}},"focus":false}`},
		{"close_window", "session.close_window", func(c *client.Client) error { _, err := c.CloseWindow(context.Background(), "s", "w"); return err }, `{"session_id":"s","window_id":"w"}`},
		{"new_split", "session.new_split", func(c *client.Client) error {
			_, err := c.NewSplit(context.Background(), client.NewSplitArgs{SessionID: "s", AnchorID: "block:a", Direction: client.Horizontal, Side: client.After, Ratio: 0.6, Focus: true,
				Layout: client.BlockLayout(client.TerminalFlavor, "info", client.TerminalOptions{Shell: "none", Command: []string{"forge", "quick", "info"}, Cwd: "/w"})})
			return err
		}, `{"session_id":"s","anchor_id":"block:a","direction":"horizontal","side":"after","ratio":0.6,"layout":{"block":{"flavor":"com.superlogical.terminal.shell","options":{"shell":"none","command":["forge","quick","info"],"cwd":"/w"},"label":"info"}},"focus":true}`},
		{"set_block_label", "session.set_block_label", func(c *client.Client) error {
			_, err := c.SetBlockLabel(context.Background(), "s", "b", "New session")
			return err
		}, `{"session_id":"s","block_id":"b","label":"New session"}`},
		{"new_layer", "session.new_layer", func(c *client.Client) error {
			_, err := c.NewLayer(context.Background(), client.NewLayerArgs{SessionID: "s", Bounds: client.Bounds{0.1, 0.2, 0.5, 0.4},
				Layout: client.BlockLayout(client.TerminalFlavor, "", client.TerminalOptions{Shell: "none", Command: []string{"x"}, Size: &client.Size{Columns: 58, Rows: 20}})})
			return err
		}, `{"session_id":"s","bounds":[0.1,0.2,0.5,0.4],"layout":{"block":{"flavor":"com.superlogical.terminal.shell","options":{"shell":"none","command":["x"],"size":{"columns":58,"rows":20}}}}}`},
		{"resize_layer", "session.resize_layer", func(c *client.Client) error {
			_, err := c.ResizeLayer(context.Background(), "s", "l", client.Bounds{0, 0, 1, 0.5})
			return err
		}, `{"session_id":"s","layer_id":"l","bounds":[0,0,1,0.5]}`},
		{"close_layer", "session.close_layer", func(c *client.Client) error { _, err := c.CloseLayer(context.Background(), "s", "l"); return err }, `{"session_id":"s","layer_id":"l"}`},
		{"raise_layer front", "session.raise_layer", func(c *client.Client) error { _, err := c.RaiseLayer(context.Background(), "s", "l", ""); return err }, `{"session_id":"s","layer_id":"l"}`},
		{"new_block", "session.new_block", func(c *client.Client) error {
			_, err := c.NewBlock(context.Background(), client.NewBlockArgs{SessionID: "s", Flavor: client.TerminalFlavor, Label: "forge-warm:new"})
			return err
		}, `{"session_id":"s","flavor":"com.superlogical.terminal.shell","label":"forge-warm:new"}`},
		{"move_block", "session.move_block", func(c *client.Client) error {
			_, err := c.MoveBlock(context.Background(), client.MoveBlockArgs{SessionID: "s", BlockID: "b", AnchorBlockID: "a"})
			return err
		}, `{"session_id":"s","block_id":"b","anchor_block_id":"a"}`},
		{"focus_block", "session.focus_block", func(c *client.Client) error { _, err := c.FocusBlock(context.Background(), "s", "b"); return err }, `{"session_id":"s","block_id":"b"}`},
		{"list_blocks", "session.list_blocks", func(c *client.Client) error { _, err := c.ListBlocks(context.Background(), "s"); return err }, `{"session_id":"s"}`},
		{"block.close", "block.close", func(c *client.Client) error { _, err := c.CloseBlock(context.Background(), "s", "b"); return err }, `{"session_id":"s","block_id":"b"}`},
		{"terminal.size", "com.superlogical.terminal.size", func(c *client.Client) error { _, err := c.TerminalSize(context.Background(), "s", "b"); return err }, `{"session_id":"s","block_id":"b","args":{}}`},
		{"session.create", "session.create", func(c *client.Client) error {
			_, err := c.CreateSession(context.Background(), client.CreateSessionArgs{Label: "proj", WindowLabel: "shell", Cwd: "/w"})
			return err
		}, `{"label":"proj","initial_windows":[{"window_label":"shell","layout":{"block":{"flavor":"com.superlogical.terminal.shell","options":{"cwd":"/w"}}}}]}`},
		{"session.create bare", "session.create", func(c *client.Client) error {
			_, err := c.CreateSession(context.Background(), client.CreateSessionArgs{})
			return err
		}, `{"initial_windows":[{"layout":{"block":{"flavor":"com.superlogical.terminal.shell"}}}]}`},
		{"session.destroy", "session.destroy", func(c *client.Client) error { return c.DestroySession(context.Background(), "s") }, `{"session_id":"s"}`},
		{"focus_window", "session.focus_window", func(c *client.Client) error { _, err := c.FocusWindow(context.Background(), "s", "w"); return err }, `{"session_id":"s","window_id":"w"}`},
		{"set_window_label", "session.set_window_label", func(c *client.Client) error {
			_, err := c.SetWindowLabel(context.Background(), "s", "w", "Needs you")
			return err
		}, `{"session_id":"s","window_id":"w","label":"Needs you"}`},
		{"set_window_label restore", "session.set_window_label", func(c *client.Client) error {
			_, err := c.SetWindowLabel(context.Background(), "s", "w", "")
			return err
		}, `{"session_id":"s","window_id":"w","label":""}`},
		{"attach_all session", "session.attach_all", func(c *client.Client) error {
			_, err := c.AttachAll(context.Background(), client.ScopeSession)
			return err
		}, `{"event_scope":"session"}`},
		{"attach_all all", "session.attach_all", func(c *client.Client) error { _, err := c.AttachAll(context.Background(), client.ScopeAll); return err }, `{"event_scope":"all"}`},
		{"client.list all", "client.list", func(c *client.Client) error { _, err := c.ListClients(context.Background(), ""); return err }, `{}`},
		{"client.list filtered", "client.list", func(c *client.Client) error {
			_, err := c.ListClients(context.Background(), "s", "app", "cli")
			return err
		}, `{"session_id":"s","kinds":["app","cli"]}`},
		{"client.perform", "client.perform", func(c *client.Client) error {
			_, err := c.Perform(context.Background(), client.PerformArgs{ClientID: "client:app", Action: "session.select", Args: map[string]string{"session_id": "s", "window_id": "w"}, Origin: "forge", TimeoutMS: 2000})
			return err
		}, `{"client_id":"client:app","action":"session.select","args":{"session_id":"s","window_id":"w"},"origin":"forge","timeout_ms":2000}`},
		{"client.perform minimal", "client.perform", func(c *client.Client) error {
			_, err := c.Perform(context.Background(), client.PerformArgs{ClientID: "client:app", Action: "pane.split"})
			return err
		}, `{"client_id":"client:app","action":"pane.split"}`},
		{"server.config_reload", "server.config_reload", func(c *client.Client) error { _, err := c.ConfigReload(context.Background()); return err }, `null`},
		{"terminal.process", "com.superlogical.terminal.process", func(c *client.Client) error { _, err := c.BlockProcess(context.Background(), "s", "b"); return err }, `{"session_id":"s","block_id":"b","args":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := clienttest.New(t)
			srv.Reply(tt.method, map[string]any{})
			c := dial(t, srv, client.Options{})
			if err := tt.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			reqs := srv.Requests()
			if len(reqs) != 1 || reqs[0].Method != tt.method {
				t.Fatalf("requests = %+v", reqs)
			}
			got := string(reqs[0].Payload)
			if got == "" {
				got = "null"
			}
			if got != tt.want {
				t.Errorf("payload\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestMethodResultsDecode(t *testing.T) {
	srv := clienttest.New(t)
	srv.Reply("session.attach", map[string]any{
		"client_id": "client:me",
		"view": map[string]any{
			"revision": 3, "session_id": "session:1", "active_window_id": "window:1",
			"windows": []any{map[string]any{
				"window_id": "window:1", "focused_block_id": "block:1",
				"layers": []any{
					map[string]any{"layer_id": "layer:t", "kind": "tiled", "bounds": []float64{0, 0, 1, 1},
						"layout": map[string]any{"rect": []float64{0, 0, 1, 1}, "block_id": "block:1"},
						"blocks": []any{map[string]any{"block_id": "block:1", "label": "shell"}}},
					map[string]any{"layer_id": "layer:f", "kind": "floating", "bounds": []float64{0.3, 0.3, 0.4, 0.4}, "layout": nil, "blocks": nil},
				},
			}},
		},
	})
	srv.Reply("session.list_blocks", map[string]any{"blocks": []any{
		map[string]any{"block_id": "block:1", "label": "shell", "placement": "placed", "window_id": "window:1"},
		map[string]any{"block_id": "block:2", "label": "forge-warm:new", "placement": "detached"},
	}})
	srv.Reply("com.superlogical.terminal.size", map[string]any{"columns": 58, "rows": 20, "cell_width_px": 8, "cell_height_px": 16, "owner": nil, "desired": []any{}})
	srv.Reply("session.new_layer", map[string]any{"layer_id": "layer:n", "block_ids": []string{"block:9"}, "revision": 12})
	c := dial(t, srv, client.Options{})
	ctx := context.Background()

	if _, err := srv.Emit(clienttest.SessionEventOf("session:1", "session_view_changed", map[string]int{"revision": 3})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "event delivery", func() bool { return len(c.Events()) == 1 })

	a, err := c.Attach(ctx, "session:1")
	if err != nil {
		t.Fatal(err)
	}
	if a.ClientID != "client:me" || a.View.Revision != 3 || a.Seq != 1 {
		t.Errorf("attach = %+v (seq %d)", a, a.Seq)
	}
	w := a.View.Windows[0]
	if len(w.Layers) != 2 || w.Layers[0].Layout.BlockID != "block:1" || w.Layers[1].Layout != nil || w.Layers[1].Bounds != (client.Bounds{0.3, 0.3, 0.4, 0.4}) {
		t.Errorf("view layers = %+v", w.Layers)
	}

	blocks, err := c.ListBlocks(ctx, "session:1")
	if err != nil || len(blocks) != 2 || blocks[1].Placement != client.Detached || blocks[1].Label != "forge-warm:new" {
		t.Errorf("ListBlocks = %+v, %v", blocks, err)
	}
	sz, err := c.TerminalSize(ctx, "session:1", "block:1")
	if err != nil || sz.Columns != 58 || sz.Rows != 20 || sz.Owner != "" {
		t.Errorf("TerminalSize = %+v, %v", sz, err)
	}
	nl, err := c.NewLayer(ctx, client.NewLayerArgs{SessionID: "session:1", Bounds: client.Bounds{0, 0, 1, 1}, Layout: client.BlockLayout(client.TerminalFlavor, "", nil)})
	if err != nil || nl.LayerID != "layer:n" || len(nl.BlockIDs) != 1 || nl.Revision != 12 {
		t.Errorf("NewLayer = %+v, %v", nl, err)
	}
}

// TestUnixSocketDial covers the default transport: a percent-encoded unix://
// address with a space in it, like the real one. It binds under /tmp (the
// test temp dir exceeds sockaddr_un's ~104 bytes on macOS) and skips where
// the sandbox denies bind().
func TestUnixSocketDial(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rk sp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("unix-socket bind denied here (sandbox): %v", err)
		}
		t.Fatal(err)
	}
	srv := clienttest.New(t)
	srv.Reply("server.version", map[string]any{"rex_version": "0.0.1", "platform": "darwin_arm64", "dirty": false, "api_revision": 7})
	hs := &http.Server{Handler: srv}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })

	addr := "unix://" + (&url.URL{Path: sock}).EscapedPath()
	if !strings.Contains(addr, "%20") {
		t.Fatalf("test address %q does not exercise decoding", addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, client.Options{Getenv: func(k string) string {
		if k == "REX_SERVER" {
			return addr
		}
		return ""
	}, PingInterval: -1})
	if err != nil {
		t.Fatalf("Dial over unix socket: %v", err)
	}
	defer func() { _ = c.Close() }()
	v, err := c.ServerVersion(ctx)
	if err != nil || v.RexVersion != "0.0.1" || v.APIRevision != 7 {
		t.Errorf("ServerVersion = %+v, %v", v, err)
	}
}

// TestNewWrapperResultsDecode pins the result shapes forge's Rex seam reads:
// the first window and block from session.create, the pid, kind and
// connection state from client.list, the answer from client.perform, and
// the child pid from the terminal process method.
func TestNewWrapperResultsDecode(t *testing.T) {
	srv := clienttest.New(t)
	srv.Reply("session.create", map[string]any{
		"session_id":      "session:new",
		"initial_windows": []any{map[string]any{"window_id": "window:1", "block_ids": []string{"block:1"}, "revision": 1}},
	})
	srv.Reply("session.attach_all", map[string]any{"sessions": []any{
		map[string]any{"session_id": "session:1", "label": "work"},
		map[string]any{"session_id": "session:2", "label": "play"},
	}})
	srv.Reply("client.list", map[string]any{"clients": []any{
		map[string]any{
			"client_id": "client:app", "info": map[string]any{"kind": "app", "name": "Rex.app", "label": "studio"},
			"principal":    map[string]any{"kind": "peer", "transport": "unix", "display": "uid 501", "uid": 501, "pid": 4242},
			"connected_at": "2026-09-27T09:30:00Z", "connection_state": "connected",
			"session_ids": []string{"session:1"},
		},
		map[string]any{
			"client_id": "client:old", "principal": map[string]any{"kind": "peer", "transport": "unix", "display": "uid 501"},
			"connected_at": "2026-09-27T09:00:00Z", "connection_state": "lost",
		},
	}})
	srv.Reply("client.perform", map[string]any{"result": map[string]any{"ok": true}, "performed_by": "client:app"})
	srv.Reply("server.config_reload", map[string]any{"generation": 3, "diagnostics": []any{
		map[string]any{"severity": "warning", "source": "init.lua:22", "message": "bad key", "key": "ctrl+a f"},
	}})
	srv.Reply("com.superlogical.terminal.process", map[string]any{
		"last_exit": nil,
		"child":     map[string]any{"pid": 777, "user_id": 501, "name": "sleep", "argv0": "sleep"},
		"home":      "/Users/x",
	})
	srv.Reply("session.focus_window", map[string]any{"revision": 9})
	c := dial(t, srv, client.Options{})
	ctx := context.Background()

	cs, err := c.CreateSession(ctx, client.CreateSessionArgs{Label: "proj"})
	if err != nil || cs.SessionID != "session:new" || cs.FirstWindowID() != "window:1" || cs.FirstBlockID() != "block:1" {
		t.Errorf("CreateSession = %+v, %v", cs, err)
	}
	if (client.CreateSessionResult{}).FirstBlockID() != "" || (client.CreateSessionResult{}).FirstWindowID() != "" {
		t.Error("empty CreateSessionResult must report no first window or block")
	}

	ss, err := c.AttachAll(ctx, client.ScopeSession)
	if err != nil || len(ss) != 2 || ss[1].Label != "play" {
		t.Errorf("AttachAll = %+v, %v", ss, err)
	}

	cl, err := c.ListClients(ctx, "")
	if err != nil || len(cl) != 2 {
		t.Fatalf("ListClients = %+v, %v", cl, err)
	}
	if a := cl[0]; a.Info.Kind != "app" || a.Principal.PID != 4242 || a.Lost() || len(a.SessionIDs) != 1 || a.SessionIDs[0] != "session:1" {
		t.Errorf("app client = %+v", a)
	}
	if o := cl[1]; !o.Lost() || o.Principal.PID != 0 || o.Info.Kind != "" {
		t.Errorf("lost client = %+v", o)
	}

	pr, err := c.Perform(ctx, client.PerformArgs{ClientID: "client:app", Action: "session.select"})
	if err != nil || pr.PerformedBy != "client:app" || string(pr.Result) != `{"ok":true}` {
		t.Errorf("Perform = %+v, %v", pr, err)
	}

	cr, err := c.ConfigReload(ctx)
	if err != nil || cr.Generation != 3 || len(cr.Diagnostics) != 1 || cr.Diagnostics[0].Source != "init.lua:22" || cr.Diagnostics[0].Key != "ctrl+a f" {
		t.Errorf("ConfigReload = %+v, %v", cr, err)
	}

	p, err := c.BlockProcess(ctx, "session:1", "block:1")
	if err != nil || p.Child == nil || p.Child.PID != 777 || p.Child.Name != "sleep" || p.LastExit != nil || p.Foreground != nil {
		t.Errorf("BlockProcess = %+v, %v", p, err)
	}

	if rev, err := c.FocusWindow(ctx, "session:1", "window:1"); err != nil || rev != 9 {
		t.Errorf("FocusWindow = %d, %v", rev, err)
	}
}

// TestNewWrappersMapNotFound pins that each wrapper that names a session,
// window or client surfaces the server's not_found and session_not_found as
// the matching sentinels, so callers can treat a gone window as success
// without matching message text. The refused row is the one forge maps to
// "Remote Control is off".
func TestNewWrappersMapNotFound(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		method string
		code   string
		status int
		want   error
		call   func(*client.Client) error
	}{
		{"focus_window", "session.focus_window", "not_found", 404, client.ErrNotFound, func(c *client.Client) error { _, err := c.FocusWindow(ctx, "s", "w"); return err }},
		{"set_window_label", "session.set_window_label", "not_found", 404, client.ErrNotFound, func(c *client.Client) error { _, err := c.SetWindowLabel(ctx, "s", "w", "x"); return err }},
		{"destroy session", "session.destroy", "session_not_found", 404, client.ErrSessionNotFound, func(c *client.Client) error { return c.DestroySession(ctx, "s") }},
		{"focus_window session", "session.focus_window", "session_not_found", 404, client.ErrSessionNotFound, func(c *client.Client) error { _, err := c.FocusWindow(ctx, "s", "w"); return err }},
		{"perform no client", "client.perform", "not_found", 404, client.ErrNotFound, func(c *client.Client) error {
			_, err := c.Perform(ctx, client.PerformArgs{ClientID: "x", Action: "a"})
			return err
		}},
		{"perform refused", "client.perform", "refused", 403, client.ErrRefused, func(c *client.Client) error {
			_, err := c.Perform(ctx, client.PerformArgs{ClientID: "x", Action: "session.select"})
			return err
		}},
		{"block process", "com.superlogical.terminal.process", "not_found", 404, client.ErrNotFound, func(c *client.Client) error { _, err := c.BlockProcess(ctx, "s", "b"); return err }},
		{"config_reload non-local", "server.config_reload", "refused", 403, client.ErrRefused, func(c *client.Client) error { _, err := c.ConfigReload(ctx); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := clienttest.New(t)
			srv.Fail(tt.method, tt.code, tt.status, "gone")
			c := dial(t, srv, client.Options{})
			err := tt.call(c)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tt.want)
			}
			for _, other := range []error{client.ErrNotFound, client.ErrSessionNotFound} {
				if other != tt.want && errors.Is(err, other) {
					t.Errorf("err also matches %v", other)
				}
			}
		})
	}
}

// TestServerTracksConnectionsAndAttachment pins the fake's multi-connection
// behaviour, which rextest builds on. Load-bearing rows: a short-lived call
// connection closing must leave an event connection reachable by Emit (the
// old single-slot fake went nil); EmitAttachAll skips a connection that
// never called attach_all; and a handler sees which connection called and
// what it attached, so a fake can fail disown_block without session.attach.
func TestServerTracksConnectionsAndAttachment(t *testing.T) {
	srv := clienttest.New(t)
	srv.Reply("session.attach_all", map[string]any{"sessions": []any{}})
	srv.Reply("session.attach", map[string]any{"client_id": "client:x", "view": map[string]any{"revision": 1, "session_id": "s", "windows": []any{}}})
	srv.Handle("session.disown_block", func(ctx context.Context, req clienttest.Request) (any, *clienttest.Error) {
		if !clienttest.CallerFrom(ctx).Has("s") {
			return nil, &clienttest.Error{Code: "conflict", Status: 409, Message: "not attached"}
		}
		return map[string]any{}, nil
	})
	ctx := context.Background()

	events := dial(t, srv, client.Options{})
	if _, err := events.AttachAll(ctx, client.ScopeAll); err != nil {
		t.Fatal(err)
	}
	plain := dial(t, srv, client.Options{})
	short := dial(t, srv, client.Options{})
	if got := srv.Connections(); got != 3 {
		t.Fatalf("Connections = %d, want 3", got)
	}

	// disown_block fails on a connection that never attached, and works
	// once it has.
	disown := func(c *client.Client) error {
		return c.Call(ctx, "session.disown_block", map[string]string{"session_id": "s", "block_id": "b"}, nil)
	}
	if err := disown(plain); !errors.Is(err, client.ErrConflict) {
		t.Fatalf("disown before attach: %v, want conflict", err)
	}
	if _, err := plain.Attach(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if err := disown(plain); err != nil {
		t.Fatalf("disown after attach: %v", err)
	}

	// Closing a short-lived connection leaves the others emit-able.
	_ = short.Close()
	waitFor(t, "short connection to leave", func() bool { return srv.Connections() == 2 })

	if _, err := srv.EmitAttachAll(clienttest.EventOf("session_created", map[string]string{"session_id": "s"})); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events.Events():
		if ev.Type != "session_created" {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach_all connection got no event")
	}
	if n := len(plain.Events()); n != 0 {
		t.Errorf("a connection that never called attach_all got %d events from EmitAttachAll", n)
	}

	if _, err := srv.Emit(clienttest.EventOf("keymap_changed", map[string]int{"generation": 2})); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*client.Client{"events": events, "plain": plain} {
		select {
		case ev := <-c.Events():
			if ev.Type != "keymap_changed" {
				t.Errorf("%s event = %+v", name, ev)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s connection got no Emit", name)
		}
	}

	callers := srv.Callers()
	if len(callers) != 2 || callers[0].EventScope != "all" || callers[1].EventScope != "" || !callers[1].Has("s") {
		t.Errorf("Callers = %+v", callers)
	}
	assertNoFakeErrs(t, srv)
}
