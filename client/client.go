// Package client speaks Rex's control protocol, rex.control.v1, over one
// WebSocket connection to the Rex server.
//
// The protocol is specified by the server itself; rexuikit keeps a snapshot in
// docs/rex-api/llms-r7.md (the "## The Control Connection" section and its
// subsections are the normative source for everything in this file). Section
// names are cited in the doc comments below so a reader can check each rule
// against the spec instead of trusting this code.
//
// A Client is safe for concurrent use. Every request gets a unique id and is
// multiplexed over the single connection; one reader goroutine routes each
// response back to its caller by id and every event onto the Events channel
// in seq order.
//
// Reconnecting is deliberately not implemented: the welcome's reconnect token
// is recorded (ReconnectToken) so a later version can resume a client, but a
// dropped connection here simply fails every pending and future call.
//
// This package never shells out to the rex CLI: the CLI costs ~76ms per call
// against 0.1-3ms over this connection, which is the reason rexuikit exists.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	// Subprotocol is the WebSocket subprotocol the control endpoint requires
	// ("## The Control Connection").
	Subprotocol = "rex.control.v1"

	ControlPath = "/api/control"

	// APIRevision is the control API revision this package was written
	// against: every typed shape in methods.go is revision 7's
	// ("### API Revisions"). The hello sends it as api_revision.
	APIRevision = 7

	// MinAPIRevision is the oldest revision this package can decode. It
	// equals APIRevision because older revisions use different shapes for
	// the same methods; asking for less would let the server translate
	// messages into shapes methods.go cannot read. A server that cannot
	// speak revision 7 closes the connection with status 1008.
	MinAPIRevision = 7

	// DefaultHandshakeTimeout bounds the wait for the welcome. The server
	// itself drops a connection whose hello is not in within 5 seconds
	// ("### Opening the Connection"), so waiting longer for its reply would
	// only hide a dead server.
	DefaultHandshakeTimeout = 5 * time.Second

	// DefaultEventBuffer is how many undelivered events the client holds
	// before it gives up on the connection (see ErrEventOverflow).
	DefaultEventBuffer = 1024

	// DefaultWriteTimeout bounds one request write. Writes are not tied to
	// the caller's context: coder/websocket closes the whole connection when
	// a write's context ends mid-write, so a caller's cancellation would
	// otherwise kill every other caller's requests too.
	DefaultWriteTimeout = 5 * time.Second

	// DefaultPingInterval and pingTimeout follow the server's own keepalive
	// schedule ("### Staying Connected"): ping every 30s, wait 15s for each
	// pong, give up after two misses in a row.
	DefaultPingInterval = 30 * time.Second
	pingTimeout         = 15 * time.Second
	pingMisses          = 2

	// helloLimit is the spec's cap on the hello's size ("### Opening the
	// Connection"). The hello is never compressed: compression is not
	// negotiated at all (see Dial), which also satisfies the spec's rule
	// that a hello carrying a reconnect token must go uncompressed.
	helloLimit = 512

	// minMessageBytes is the floor every server allows ("### Message
	// Size"); it is also the read limit used until the welcome names the
	// real one.
	minMessageBytes = 65536

	// handshakeReadLimit is the read limit while waiting for the welcome.
	// coder/websocket defaults to 32 KiB, and a welcome listing carriers and
	// features is small, but the limit only has to stop a hostile peer.
	handshakeReadLimit = 1 << 20
)

// DefaultSocketPath is where the macOS Rex app's server listens when
// REX_SERVER is not set, relative to $HOME.
const DefaultSocketPath = "Library/Application Support/rex/server.sock"

var (
	// ErrClosed is returned by calls made after, or pending during, Close.
	ErrClosed = errors.New("rexuikit/client: connection closed")

	// ErrEventOverflow is the connection's terminal error when the event
	// buffer fills because nobody is reading Events. The client closes the
	// connection rather than drop an event: an event stream with a hole
	// silently desynchronises every view built from it, while a closed
	// connection is a visible failure the caller can recover from by
	// reconnecting and re-reading state ("### Combining Results With
	// Events").
	ErrEventOverflow = errors.New("rexuikit/client: event buffer full (Events not drained); connection closed")

	// ErrEventGap is the terminal error when an event's seq is not exactly
	// one more than the last. The server promises a gapless sequence on a
	// live connection ("### Combining Results With Events"), so a gap means
	// an event was lost and any state built from the stream is wrong.
	ErrEventGap = errors.New("rexuikit/client: event seq out of order")

	// ErrKeepalive is the terminal error after two unanswered pings.
	ErrKeepalive = errors.New("rexuikit/client: server stopped answering pings")
)

// Options configures Dial. The zero value dials the server named by the
// environment (REX_SERVER, else the default macOS socket).
type Options struct {
	// Addr is the server address: a unix:// URL whose path is
	// percent-encoded (REX_SERVER's format, e.g.
	// unix:///Users/me/Library/Application%20Support/rex/server.sock), or
	// an http://, https://, ws:// or wss:// base URL. Empty resolves it
	// from Getenv with ServerAddr.
	Addr string

	// HTTPClient carries the WebSocket handshake. Nil builds one that dials
	// Addr's unix socket (or uses http.DefaultClient for a network URL).
	// Tests pass an httptest server's client to run hermetically over TCP
	// loopback, because the sandbox denies unix-socket bind().
	HTTPClient *http.Client

	// Getenv resolves an empty Addr. Nil means os.Getenv.
	Getenv func(string) string

	// Name and Version describe this program in the hello's client object.
	// Empty Name means "rexuikit".
	Name    string
	Version string

	// HandshakeTimeout bounds the wait for the welcome; zero means
	// DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration

	// EventBuffer is how many events may queue before the connection is
	// failed with ErrEventOverflow; zero means DefaultEventBuffer.
	EventBuffer int

	// DisableEvents discards every event instead of queueing it, and makes
	// Events return a nil channel. Use it for a connection that only makes
	// calls: an attached session emits events continuously, and an
	// undrained buffer would otherwise end the connection.
	DisableEvents bool

	// PingInterval is how often the client pings the server; zero means
	// DefaultPingInterval, negative disables client pings (the client still
	// answers the server's pings).
	PingInterval time.Duration
}

// Welcome is what the server said when it accepted the connection
// ("### Opening the Connection", the welcome's fields).
type Welcome struct {
	Protocol          string        `json:"protocol"`
	Limits            Limits        `json:"limits"`
	Client            *WelcomeInfo  `json:"client,omitempty"`
	Features          []string      `json:"features,omitempty"`
	APIRevision       int           `json:"api_revision"`
	APIServerRevision int           `json:"api_server_revision"`
	APIMinRevision    int           `json:"api_min_revision"`
	Carriers          []CarrierInfo `json:"carriers,omitempty"`
}

type Limits struct {
	MaxMessageBytes int `json:"max_message_bytes"`
}

type WelcomeInfo struct {
	ID             string `json:"id"`
	ReconnectToken string `json:"reconnect_token"`
	Resumed        bool   `json:"resumed"`
}

// CarrierInfo is another transport to the same server. rexuikit ignores
// carriers; the field is kept so Welcome round-trips what the server sent.
type CarrierInfo struct {
	Carrier string   `json:"carrier"`
	Port    int      `json:"port"`
	ALPN    []string `json:"alpn"`
}

// Event is one event message from the server ("### Events"): its place in
// the connection's sequence and the inner event's name and fields. Unknown
// types are delivered unchanged; the spec requires clients to skip them, and
// that choice belongs to the consumer, not the transport.
type Event struct {
	Seq     int64
	Type    string
	Payload json.RawMessage
}

// SessionEvent is the inside of a session_event: the session it happened in
// and the event itself.
type SessionEvent struct {
	SessionID string
	Type      string
	Payload   json.RawMessage
}

// BlockEvent is a block's own event, which arrives as a block_event inside a
// session_event ("### Events"; fields per "### `block_event`").
type BlockEvent struct {
	SessionID   string
	BlockID     string
	CreatorName string
	Name        string
	Payload     json.RawMessage
}

// Session unwraps a session_event. ok is false for any other event type.
func (e Event) Session() (se SessionEvent, ok bool, err error) {
	if e.Type != "session_event" {
		return SessionEvent{}, false, nil
	}
	var w struct {
		SessionID string `json:"session_id"`
		Event     struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		} `json:"event"`
	}
	if err := json.Unmarshal(e.Payload, &w); err != nil {
		return SessionEvent{}, true, fmt.Errorf("rexuikit/client: decode session_event: %w", err)
	}
	return SessionEvent{SessionID: w.SessionID, Type: w.Event.Type, Payload: w.Event.Payload}, true, nil
}

// Block unwraps a block_event carried in a session_event. ok is false for
// every other event.
func (e Event) Block() (be BlockEvent, ok bool, err error) {
	se, ok, err := e.Session()
	if err != nil || !ok || se.Type != "block_event" {
		return BlockEvent{}, false, err
	}
	var w struct {
		BlockID     string          `json:"block_id"`
		CreatorName string          `json:"creator_name"`
		Name        string          `json:"name"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(se.Payload, &w); err != nil {
		return BlockEvent{}, true, fmt.Errorf("rexuikit/client: decode block_event: %w", err)
	}
	return BlockEvent{
		SessionID:   se.SessionID,
		BlockID:     w.BlockID,
		CreatorName: w.CreatorName,
		Name:        w.Name,
		Payload:     w.Payload,
	}, true, nil
}

type Client struct {
	conn    *websocket.Conn
	welcome Welcome
	maxSend int

	nextID atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan reply
	err     error

	done      chan struct{}
	readerOut chan struct{}
	events    chan Event
	closeOnce sync.Once
}

type reply struct {
	seq     int64
	payload json.RawMessage
	err     *Error
}

// envelope is every server message's shape; which fields are set depends on
// type ("## The Control Connection": welcome, response, event).
type envelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Seq     int64           `json:"seq"`
	Payload json.RawMessage `json:"payload"`
	Error   *Error          `json:"error"`
}

type hello struct {
	Type           string     `json:"type"`
	APIRevision    int        `json:"api_revision"`
	APIMinRevision int        `json:"api_min_revision"`
	Client         helloAbout `json:"client"`
}

type helloAbout struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type request struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Payload any    `json:"payload,omitempty"`
}

// ServerAddr resolves the server address from the environment: REX_SERVER
// when set, else a unix:// URL for $HOME's default socket. It does no I/O.
func ServerAddr(getenv func(string) string) (string, error) {
	if v := getenv("REX_SERVER"); v != "" {
		return v, nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("rexuikit/client: REX_SERVER and HOME are both unset")
	}
	return "unix://" + (&url.URL{Path: filepath.Join(home, DefaultSocketPath)}).EscapedPath(), nil
}

// SocketPath returns the filesystem path a unix:// address names. Rex
// percent-encodes the path in REX_SERVER (a space in "Application Support"
// arrives as %20), so the path is unescaped exactly once; the snapshot script
// rexuikit/scripts/rex-api-snapshot.sh decodes it the same way.
func SocketPath(addr string) (string, error) {
	rest, ok := strings.CutPrefix(addr, "unix://")
	if !ok {
		return "", fmt.Errorf("rexuikit/client: not a unix:// address: %q", addr)
	}
	p, err := url.PathUnescape(rest)
	if err != nil {
		return "", fmt.Errorf("rexuikit/client: decode socket path %q: %w", addr, err)
	}
	if p == "" {
		return "", fmt.Errorf("rexuikit/client: empty socket path in %q", addr)
	}
	return p, nil
}

func endpoint(addr string) (wsURL, socket string, err error) {
	if strings.HasPrefix(addr, "unix://") {
		socket, err = SocketPath(addr)
		if err != nil {
			return "", "", err
		}
		// The host is never resolved: the transport ignores it and dials
		// the socket. It only fills the Host header.
		return "ws://rex" + ControlPath, socket, nil
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", "", fmt.Errorf("rexuikit/client: parse address %q: %w", addr, err)
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", "", fmt.Errorf("rexuikit/client: unsupported address scheme %q in %q", u.Scheme, addr)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + ControlPath
	return u.String(), "", nil
}

func unixHTTPClient(socket string) *http.Client {
	var d net.Dialer
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", socket)
		},
	}}
}

// Dial opens a control connection, sends the hello and waits for the
// welcome. ctx bounds the whole handshake; the returned Client outlives it.
func Dial(ctx context.Context, opts Options) (*Client, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	addr := opts.Addr
	if addr == "" {
		var err error
		if addr, err = ServerAddr(getenv); err != nil {
			return nil, err
		}
	}
	wsURL, socket, err := endpoint(addr)
	if err != nil {
		return nil, err
	}
	hc := opts.HTTPClient
	if hc == nil && socket != "" {
		hc = unixHTTPClient(socket)
	}

	name := opts.Name
	if name == "" {
		name = "rexuikit"
	}
	hb, err := json.Marshal(hello{
		Type:           "hello",
		APIRevision:    APIRevision,
		APIMinRevision: MinAPIRevision,
		Client:         helloAbout{Kind: "cli", Name: name, Version: opts.Version},
	})
	if err != nil {
		return nil, err
	}
	if len(hb) >= helloLimit {
		return nil, fmt.Errorf("rexuikit/client: hello is %d bytes, the server allows under %d (shorten Name/Version)", len(hb), helloLimit)
	}

	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Compression stays disabled (the library default): the server works
	// without it ("## The Control Connection"), local messages are small,
	// and it keeps the hello uncompressed as the spec requires.
	conn, _, err := websocket.Dial(hctx, wsURL, &websocket.DialOptions{
		HTTPClient:   hc,
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return nil, fmt.Errorf("rexuikit/client: dial %s: %w", addr, err)
	}
	// coder/websocket accepts a response with no subprotocol at all; the
	// server always selects rex.control.v1, so anything else is not Rex.
	if got := conn.Subprotocol(); got != Subprotocol {
		_ = conn.CloseNow()
		return nil, fmt.Errorf("rexuikit/client: server selected subprotocol %q, want %q", got, Subprotocol)
	}

	welcome, err := handshake(hctx, conn, hb)
	if err != nil {
		_ = conn.CloseNow()
		return nil, err
	}

	maxSend := welcome.Limits.MaxMessageBytes
	if maxSend < minMessageBytes {
		maxSend = minMessageBytes
	}
	conn.SetReadLimit(int64(maxSend))

	c := &Client{
		conn:      conn,
		welcome:   welcome,
		maxSend:   maxSend,
		pending:   map[string]chan reply{},
		done:      make(chan struct{}),
		readerOut: make(chan struct{}),
	}
	if !opts.DisableEvents {
		n := opts.EventBuffer
		if n <= 0 {
			n = DefaultEventBuffer
		}
		c.events = make(chan Event, n)
	}
	go c.read()
	if iv := opts.PingInterval; iv >= 0 {
		if iv == 0 {
			iv = DefaultPingInterval
		}
		go c.keepalive(iv)
	}
	return c, nil
}

// handshake sends the hello and validates the welcome. The spec says to
// check type and protocol before sending anything else ("### Opening the
// Connection"), and that a server unable to serve the client closes instead
// of welcoming it, which surfaces here as the read's close error.
func handshake(ctx context.Context, conn *websocket.Conn, hb []byte) (Welcome, error) {
	conn.SetReadLimit(handshakeReadLimit)
	if err := conn.Write(ctx, websocket.MessageText, hb); err != nil {
		return Welcome{}, fmt.Errorf("rexuikit/client: send hello: %w", err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil {
		if s := websocket.CloseStatus(err); s != -1 {
			return Welcome{}, fmt.Errorf("rexuikit/client: server closed the connection instead of welcoming (status %d): %w", s, err)
		}
		return Welcome{}, fmt.Errorf("rexuikit/client: no welcome: %w", err)
	}
	if typ != websocket.MessageText {
		return Welcome{}, errors.New("rexuikit/client: first message is binary, want a welcome")
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Welcome{}, fmt.Errorf("rexuikit/client: decode welcome: %w", err)
	}
	if head.Type != "welcome" {
		return Welcome{}, fmt.Errorf("rexuikit/client: first message has type %q, want welcome", head.Type)
	}
	var w Welcome
	if err := json.Unmarshal(data, &w); err != nil {
		return Welcome{}, fmt.Errorf("rexuikit/client: decode welcome: %w", err)
	}
	if w.Protocol != Subprotocol {
		return Welcome{}, fmt.Errorf("rexuikit/client: welcome protocol %q, want %q", w.Protocol, Subprotocol)
	}
	// The server picks a revision both sides speak, and closes the
	// connection when there is none ("### API Revisions"). A welcome outside
	// our range would mean a server that ignored api_min_revision; refusing
	// it beats decoding the wrong shapes.
	if w.APIRevision < MinAPIRevision || w.APIRevision > APIRevision {
		return Welcome{}, fmt.Errorf("rexuikit/client: server chose API revision %d, this client speaks %d..%d", w.APIRevision, MinAPIRevision, APIRevision)
	}
	return w, nil
}

func (c *Client) Welcome() Welcome { return c.welcome }

func (c *Client) APIRevision() int { return c.welcome.APIRevision }

// ClientID is this connection's client id, or "" from a server that cannot
// resume clients.
func (c *Client) ClientID() string {
	if c.welcome.Client == nil {
		return ""
	}
	return c.welcome.Client.ID
}

// ReconnectToken is the token a later hello would send to resume this
// client ("### Reconnecting"). Resuming is not implemented yet; the token is
// kept for it. Never log it: it is a credential for this client.
func (c *Client) ReconnectToken() string {
	if c.welcome.Client == nil {
		return ""
	}
	return c.welcome.Client.ReconnectToken
}

// Events returns the event stream. It is closed when the connection ends;
// Err then says why. It is nil when Options.DisableEvents was set.
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends one request and decodes its result into out (which may be nil
// to discard it). payload is the method's arguments; nil omits the field, as
// the spec allows for methods that take none ("### Calling a Method").
//
// A failed call returns *Error; compare it with errors.Is against the Err*
// code sentinels. Cancelling ctx abandons the wait, not the request: the
// server may still run it, and its late response is discarded.
func (c *Client) Call(ctx context.Context, method string, payload, out any) error {
	_, err := c.CallSeq(ctx, method, payload, out)
	return err
}

// CallSeq is Call that also returns the response's seq: the last event the
// server had queued before it started the request. Events with a higher seq
// may not be reflected in the result ("### Combining Results With Events").
func (c *Client) CallSeq(ctx context.Context, method string, payload, out any) (int64, error) {
	if method == "" {
		return 0, errors.New("rexuikit/client: empty method")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	b, err := json.Marshal(request{Type: "request", ID: id, Method: method, Payload: payload})
	if err != nil {
		return 0, fmt.Errorf("rexuikit/client: encode %s: %w", method, err)
	}
	// An oversized request makes the server close the whole connection
	// with status 1009 ("### Message Size"); refusing it here keeps the
	// connection alive for everyone else.
	if len(b) > c.maxSend {
		return 0, fmt.Errorf("rexuikit/client: %s request is %d bytes, the server accepts at most %d", method, len(b), c.maxSend)
	}

	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return 0, err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	wctx, cancel := context.WithTimeout(context.Background(), DefaultWriteTimeout)
	err = c.conn.Write(wctx, websocket.MessageText, b)
	cancel()
	if err != nil {
		c.forget(id)
		// A failed write has already torn the connection down inside
		// coder/websocket; record that as the terminal error.
		c.fail(fmt.Errorf("rexuikit/client: send %s: %w", method, err))
		return 0, c.Err()
	}

	select {
	case r := <-ch:
		return r.seq, r.decode(method, out)
	case <-ctx.Done():
		c.forget(id)
		return 0, ctx.Err()
	case <-c.done:
		// The reader may have delivered the reply just before failing.
		select {
		case r := <-ch:
			return r.seq, r.decode(method, out)
		default:
		}
		return 0, c.Err()
	}
}

func (r reply) decode(method string, out any) error {
	if r.err != nil {
		r.err.Method = method
		return r.err
	}
	if out != nil && len(r.payload) > 0 {
		if err := json.Unmarshal(r.payload, out); err != nil {
			return fmt.Errorf("rexuikit/client: decode %s result: %w", method, err)
		}
	}
	return nil
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Close ends the connection with a close frame, which also ends the client
// at once on the server ("### Reconnecting"). Pending calls return ErrClosed.
// It is safe to call more than once.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	err := c.conn.Close(websocket.StatusNormalClosure, "")
	<-c.readerOut
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// fail records the terminal error and wakes every waiter through done. The
// first error wins, so Close's ErrClosed is not overwritten by the read
// error its own close frame causes.
func (c *Client) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.pending = map[string]chan reply{}
		c.mu.Unlock()
		close(c.done)
	})
}

// read is the only reader of the connection. It never blocks on a consumer:
// replies go to buffered per-call channels and events to the bounded event
// buffer, whose overflow ends the connection (ErrEventOverflow).
func (c *Client) read() {
	defer close(c.readerOut)
	if c.events != nil {
		defer close(c.events)
	}
	var lastSeq int64
	for {
		typ, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.fail(readErr(err))
			_ = c.conn.CloseNow()
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			c.fail(fmt.Errorf("rexuikit/client: undecodable message from server: %w", err))
			_ = c.conn.CloseNow()
			return
		}
		switch env.Type {
		case "response":
			c.mu.Lock()
			ch := c.pending[env.ID]
			delete(c.pending, env.ID)
			c.mu.Unlock()
			if ch != nil { // nil: the caller gave up waiting
				ch <- reply{seq: env.Seq, payload: env.Payload, err: env.Error}
			}
		case "event":
			if env.Seq != lastSeq+1 {
				c.fail(fmt.Errorf("%w: got seq %d after %d", ErrEventGap, env.Seq, lastSeq))
				_ = c.conn.CloseNow()
				return
			}
			lastSeq = env.Seq
			if c.events == nil {
				continue
			}
			var inner struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(env.Payload, &inner); err != nil {
				c.fail(fmt.Errorf("rexuikit/client: decode event %d: %w", env.Seq, err))
				_ = c.conn.CloseNow()
				return
			}
			select {
			case c.events <- Event{Seq: env.Seq, Type: inner.Type, Payload: inner.Payload}:
			default:
				c.fail(ErrEventOverflow)
				_ = c.conn.Close(websocket.StatusPolicyViolation, "client event buffer full")
				return
			}
		default:
			// Unknown message types are skipped, as the spec requires of
			// clients ("## The Control Connection").
		}
	}
}

func readErr(err error) error {
	if s := websocket.CloseStatus(err); s != -1 {
		return fmt.Errorf("rexuikit/client: server closed the connection (status %d): %w", s, err)
	}
	return fmt.Errorf("rexuikit/client: connection lost: %w", err)
}

// keepalive pings the server on its own schedule so the client notices a
// server that is gone without closing the socket ("### Staying Connected").
func (c *Client) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	misses := 0
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		err := c.conn.Ping(ctx)
		cancel()
		if err == nil {
			misses = 0
			continue
		}
		select {
		case <-c.done:
			return
		default:
		}
		if misses++; misses >= pingMisses {
			c.fail(ErrKeepalive)
			_ = c.conn.CloseNow()
			return
		}
	}
}

// Error is a failed call's error object ("### Errors"). Branch on it with
// errors.Is and the Err* sentinels, never on Message: the server rewords
// messages between releases.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
	// Method is the method that failed, filled in by the client.
	Method string `json:"-"`
}

func (e *Error) Error() string {
	if e.Method != "" {
		return fmt.Sprintf("rex %s: %s (%s, %d)", e.Method, e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("rex: %s (%s, %d)", e.Message, e.Code, e.Status)
}

// Is matches the sentinel for e's code. A code this package does not know
// matches by status instead, as the spec directs ("Handle a code you don't
// know by its status"), so a future 404 code still satisfies ErrNotFound.
func (e *Error) Is(target error) bool {
	s, ok := target.(*codeSentinel)
	if !ok {
		return false
	}
	if _, known := knownCodes[e.Code]; known {
		return s.code == e.Code
	}
	return s.code == statusFallback[e.Status]
}

type codeSentinel struct{ code string }

func (s *codeSentinel) Error() string { return "rex error code " + s.code }

// The sentinels for every code in the r7 error table. Servers released
// before method_not_found existed answer a missing method with not_found,
// so a caller probing for a method should accept both.
var (
	ErrBadRequest        error = &codeSentinel{"bad_request"}
	ErrNotFound          error = &codeSentinel{"not_found"}
	ErrMethodNotFound    error = &codeSentinel{"method_not_found"}
	ErrSessionNotFound   error = &codeSentinel{"session_not_found"}
	ErrConflict          error = &codeSentinel{"conflict"}
	ErrNotSupported      error = &codeSentinel{"not_supported"}
	ErrRefused           error = &codeSentinel{"refused"}
	ErrClientUnavailable error = &codeSentinel{"client_unavailable"}
	ErrTooLarge          error = &codeSentinel{"too_large"}
	ErrRevisionTooLow    error = &codeSentinel{"revision_too_low"}
	ErrTimeout           error = &codeSentinel{"timeout"}
	ErrInternal          error = &codeSentinel{"internal"}
)

var knownCodes = map[string]struct{}{
	"bad_request": {}, "not_found": {}, "method_not_found": {}, "session_not_found": {},
	"conflict": {}, "not_supported": {}, "refused": {}, "client_unavailable": {},
	"too_large": {}, "revision_too_low": {}, "timeout": {}, "internal": {},
}

// statusFallback maps a status to the generic code an unknown code with
// that status is treated as.
var statusFallback = map[int]string{
	400: "bad_request",
	403: "refused",
	404: "not_found",
	409: "conflict",
	413: "too_large",
	500: "internal",
	504: "timeout",
}
