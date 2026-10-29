// Package clienttest is an in-process fake of Rex's control endpoint for
// hermetic tests of rexuikit/client and the packages built on it.
//
// It serves over httptest's TCP loopback rather than a unix socket because
// the sandbox the default test run uses denies unix-socket bind(); a unix
// fake would fail every sandboxed `make check`. Server is also an
// http.Handler, so a test that can bind a unix socket may serve it there.
//
// The fake speaks the wire protocol from docs/rex-api/llms-r7.md ("## The
// Control Connection") and nothing more: methods answer only what a test
// registers with Handle, and everything else gets method_not_found.
//
// It accepts any number of simultaneous connections, as the real server
// does (a short-lived call connection closing must not hide a long-lived
// event connection): Emit reaches all of them, EmitAttachAll only those
// that called session.attach_all, and a handler can see its caller with
// CallerFrom.
//
// It deliberately does not import rexuikit/client, so tests inside package
// client can use it without an import cycle.
package clienttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const Subprotocol = "rex.control.v1"

type Hello struct {
	Type           string          `json:"type"`
	APIRevision    int             `json:"api_revision"`
	APIMinRevision int             `json:"api_min_revision"`
	ReconnectToken string          `json:"reconnect_token,omitempty"`
	Client         json.RawMessage `json:"client"`
	Raw            []byte          `json:"-"`
}

type Request struct {
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// Error is a scripted error response ("### Errors").
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// HandlerFunc answers one request. It runs on its own goroutine, so a
// handler may block (on ctx or a test channel) to force responses out of
// order. ctx ends when the connection does. A non-nil *Error becomes an error
// response; result is marshalled as the payload otherwise.
type HandlerFunc func(ctx context.Context, req Request) (result any, err *Error)

// Close, returned from a WelcomeFunc, makes the fake close the connection
// with this status instead of welcoming the client, as the real server does
// for no common API revision (status 1008).
type Close struct {
	Code   websocket.StatusCode
	Reason string
}

// WelcomeFunc chooses the server's first message for a hello. Return a
// value to marshal and send, a Close to refuse the client, or nil to send
// nothing at all (a server that never welcomes).
type WelcomeFunc func(Hello) any

// Server is the fake. Configure it before the client dials.
type Server struct {
	URL  string
	http *httptest.Server

	mu       sync.Mutex
	handlers map[string]HandlerFunc
	welcome  WelcomeFunc
	hellos   []Hello
	requests []Request
	errs     []error
	conns    []*serverConn // live connections, oldest first
	connCh   chan struct{}
	nextConn int
}

type serverConn struct {
	id      int
	ws      *websocket.Conn
	ctx     context.Context
	writeMu sync.Mutex
	seqMu   sync.Mutex
	seq     int64

	// Guarded by Server.mu. A successful session.attach_all sets scope,
	// a successful session.attach adds to attached.
	scope    string
	attached map[string]bool
}

// Caller describes the connection a request arrived on, as the fake has
// seen it so far: what it attached to and the event scope it asked for.
// A handler reads it with CallerFrom, to model the server's rule that
// session.disown_block fails with conflict on a connection that never
// called session.attach.
type Caller struct {
	ID         int      // 1 for the first connection the server accepted
	EventScope string   // "" until the connection calls session.attach_all
	Attached   []string // sessions this connection called session.attach for
}

// Has reports whether the connection called session.attach for sessionID.
func (c Caller) Has(sessionID string) bool {
	for _, id := range c.Attached {
		if id == sessionID {
			return true
		}
	}
	return false
}

type callerKey struct{}

// CallerFrom returns the connection a handler's request arrived on. The
// zero Caller means ctx did not come from a handler.
func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	return c
}

// snapshot copies the connection's attach state; the caller holds s.mu.
func (c *serverConn) snapshot() Caller {
	ids := make([]string, 0, len(c.attached))
	for id := range c.attached {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return Caller{ID: c.id, EventScope: c.scope, Attached: ids}
}

// New starts a fake on TCP loopback and closes it when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		handlers: map[string]HandlerFunc{},
		welcome:  DefaultWelcome,
		connCh:   make(chan struct{}),
	}
	s.http = httptest.NewServer(s)
	s.URL = s.http.URL
	t.Cleanup(s.Close)
	return s
}

// DefaultWelcome is the welcome a revision-7 server sends.
func DefaultWelcome(Hello) any {
	return map[string]any{
		"type":                "welcome",
		"protocol":            Subprotocol,
		"limits":              map[string]any{"max_message_bytes": 1 << 20},
		"client":              map[string]any{"id": "client:fake", "reconnect_token": "token-fake", "resumed": false},
		"api_revision":        7,
		"api_server_revision": 7,
		"api_min_revision":    0,
	}
}

func (s *Server) HTTPClient() *http.Client { return s.http.Client() }

func (s *Server) Close() {
	s.mu.Lock()
	conns := append([]*serverConn(nil), s.conns...)
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.CloseNow()
	}
	s.http.Close()
}

// SetWelcome replaces the welcome behaviour for the next connection.
func (s *Server) SetWelcome(f WelcomeFunc) {
	s.mu.Lock()
	s.welcome = f
	s.mu.Unlock()
}

func (s *Server) Handle(method string, h HandlerFunc) {
	s.mu.Lock()
	s.handlers[method] = h
	s.mu.Unlock()
}

// Reply registers a handler that always returns result.
func (s *Server) Reply(method string, result any) {
	s.Handle(method, func(context.Context, Request) (any, *Error) { return result, nil })
}

// Fail registers a handler that always fails with the given error.
func (s *Server) Fail(method, code string, status int, message string) {
	s.Handle(method, func(context.Context, Request) (any, *Error) {
		return nil, &Error{Code: code, Status: status, Message: message}
	})
}

func (s *Server) Hellos() []Hello {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Hello(nil), s.hellos...)
}

// Requests returns every request received, in arrival order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Errs returns protocol violations and I/O failures the fake saw. The fake
// never calls t.Fatal from its goroutines (that would hang the test); tests
// assert on this instead.
func (s *Server) Errs() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.errs...)
}

func (s *Server) recordErr(err error) {
	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()
}

// WaitConnected blocks until at least one client is connected, or ctx ends.
func (s *Server) WaitConnected(ctx context.Context) error {
	s.mu.Lock()
	ch, connected := s.connCh, len(s.conns) > 0
	s.mu.Unlock()
	if connected {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// current is the newest live connection, the one Send, Drop and CloseConn
// act on: after a client reconnects, that is the new one.
func (s *Server) current() (*serverConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		return nil, errors.New("clienttest: no connected client")
	}
	return s.conns[len(s.conns)-1], nil
}

// Connections is the number of live connections.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Callers returns the attach state of every live connection, oldest first.
func (s *Server) Callers() []Caller {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Caller, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c.snapshot())
	}
	return out
}

// Emit sends an event to every live connection, each with its own next seq
// (seq is per connection), and returns the seq of the newest connection's
// copy. event is the inner event, {type, payload}; build one with EventOf,
// SessionEventOf or BlockEventOf. It fails when no client is connected.
func (s *Server) Emit(event any) (int64, error) {
	return s.emit(event, func(*serverConn) bool { return true })
}

// EmitAttachAll is Emit limited to connections that called
// session.attach_all, the only ones the real server sends session-wide
// events to. It does not model event_scope filtering of individual event
// types: a test that cares emits only what its scope would receive.
func (s *Server) EmitAttachAll(event any) (int64, error) {
	return s.emit(event, func(c *serverConn) bool { return c.scope != "" })
}

func (s *Server) emit(event any, want func(*serverConn) bool) (int64, error) {
	s.mu.Lock()
	var targets []*serverConn
	for _, c := range s.conns {
		if want(c) {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	if len(targets) == 0 {
		return 0, errors.New("clienttest: no connected client")
	}
	var seq int64
	var first error
	for _, c := range targets {
		n, err := c.emit(event)
		seq = n
		if err != nil && first == nil {
			first = err
		}
	}
	return seq, first
}

func (c *serverConn) emit(event any) (int64, error) {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.seq++
	return c.seq, c.send(map[string]any{"type": "event", "seq": c.seq, "payload": event})
}

// Send writes an arbitrary message, bypassing the fake's bookkeeping. Tests
// use it for malformed or out-of-sequence traffic.
func (s *Server) Send(msg any) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.send(msg)
}

// Drop closes the client's connection without a close frame, as a crashed
// server or a lost network would.
func (s *Server) Drop() error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.ws.CloseNow()
}

func (s *Server) CloseConn(code websocket.StatusCode, reason string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.ws.Close(code, reason)
}

// EventOf is a top-level event such as session_created.
func EventOf(typ string, payload any) any {
	return map[string]any{"type": typ, "payload": payload}
}

// SessionEventOf wraps an event in a session_event ("### Events").
func SessionEventOf(sessionID, typ string, payload any) any {
	return EventOf("session_event", map[string]any{
		"session_id": sessionID,
		"event":      EventOf(typ, payload),
	})
}

// BlockEventOf is a block's own event inside a session_event.
func BlockEventOf(sessionID, blockID, creator, name string, payload any) any {
	return SessionEventOf(sessionID, "block_event", map[string]any{
		"block_id":     blockID,
		"creator_name": creator,
		"name":         name,
		"payload":      payload,
	})
}

// noteAttach records what a successful attach-family call did to the
// connection. The fake trusts the registered handler for success and only
// tracks the state the real server would keep.
func (s *Server) noteAttach(c *serverConn, req Request) {
	var p struct {
		SessionID  string `json:"session_id"`
		EventScope string `json:"event_scope"`
	}
	_ = json.Unmarshal(req.Payload, &p)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Method {
	case "session.attach":
		c.attached[p.SessionID] = true
	case "session.detach":
		delete(c.attached, p.SessionID)
	case "session.attach_all":
		c.scope = p.EventScope
	}
}

func (c *serverConn) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/control" {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		s.recordErr(fmt.Errorf("accept: %w", err))
		return
	}
	// The real server closes with 1002 when the subprotocol is missing.
	if ws.Subprotocol() != Subprotocol {
		_ = ws.Close(websocket.StatusProtocolError, "protocol error")
		return
	}
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	typ, data, err := ws.Read(hctx)
	hcancel()
	if err != nil || typ != websocket.MessageText {
		_ = ws.CloseNow()
		return
	}
	var h Hello
	if err := json.Unmarshal(data, &h); err != nil || h.Type != "hello" {
		_ = ws.Close(websocket.StatusProtocolError, "protocol error")
		return
	}
	h.Raw = data

	c := &serverConn{ws: ws, ctx: ctx, attached: map[string]bool{}}
	s.mu.Lock()
	s.nextConn++
	c.id = s.nextConn
	s.hellos = append(s.hellos, h)
	welcome := s.welcome
	s.mu.Unlock()

	switch m := welcome(h).(type) {
	case nil:
		// Never welcome: hold the connection until the client gives up.
		_, _, _ = ws.Read(ctx)
		_ = ws.CloseNow()
		return
	case Close:
		_ = ws.Close(m.Code, m.Reason)
		return
	default:
		// Register before the welcome goes out: the client's Dial returns as
		// soon as it reads the welcome, so a test that emits right after Dial
		// would otherwise race this goroutine and see "no connected client".
		s.mu.Lock()
		s.conns = append(s.conns, c)
		close(s.connCh)
		s.connCh = make(chan struct{})
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			for i, o := range s.conns {
				if o == c {
					s.conns = append(s.conns[:i:i], s.conns[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
		}()
		if err := c.send(m); err != nil {
			s.recordErr(fmt.Errorf("send welcome: %w", err))
			return
		}
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = ws.Close(websocket.StatusUnsupportedData, "protocol error")
			return
		}
		var req struct {
			Type string `json:"type"`
			Request
		}
		if err := json.Unmarshal(data, &req); err != nil || req.Type != "request" || req.ID == "" || req.Method == "" {
			s.recordErr(fmt.Errorf("bad request message: %s", data))
			_ = ws.Close(websocket.StatusProtocolError, "protocol error")
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, req.Request)
		h := s.handlers[req.Method]
		s.mu.Unlock()

		// A response's seq is the last event queued before the request
		// started ("### Calling a Method").
		c.seqMu.Lock()
		seq := c.seq
		c.seqMu.Unlock()

		wg.Add(1)
		go func(req Request, h HandlerFunc, seq int64) {
			defer wg.Done()
			resp := map[string]any{"type": "response", "id": req.ID, "seq": seq}
			if h == nil {
				resp["error"] = &Error{Code: "method_not_found", Status: 404, Message: "unknown method " + req.Method}
			} else {
				s.mu.Lock()
				caller := c.snapshot()
				s.mu.Unlock()
				result, e := h(context.WithValue(ctx, callerKey{}, caller), req)
				if e != nil {
					resp["error"] = e
				} else {
					s.noteAttach(c, req)
					resp["payload"] = result
				}
			}
			if err := c.send(resp); err != nil && ctx.Err() == nil {
				s.recordErr(fmt.Errorf("send response %s: %w", req.ID, err))
			}
		}(req.Request, h, seq)
	}
}
