// Package entry dispatches fast-path entry points before any heavy
// initialization runs, and records time to first paint.
//
// A warm Rex overlay, a Go process and a bubbletea first frame cost about 85ms
// before any application work. Everything a fast path does before its first
// paint adds to that floor, so main calls Dispatch before touching discovery,
// git, watchers or config, and defers that work until MarkPainted.
//
//	if code, ok := router.Dispatch(os.Args[1:]); ok {
//		os.Exit(code)
//	}
package entry

import (
	"context"
	"fmt"
	"strings"
)

// Func is a fast-path boot function. Its return value is the exit status.
type Func func(ctx context.Context, args []string) int

// Router maps entry names to boot functions. The zero value is ready to use.
type Router struct {
	routes map[string]Func
}

// Handle registers fn under name. Names with whitespace or '=' are rejected
// because they would corrupt the TTFP line.
func (r *Router) Handle(name string, fn Func) {
	if name == "" || strings.ContainsAny(name, " \t\r\n=") {
		panic(fmt.Sprintf("entry: invalid entry name %q", name))
	}
	if fn == nil {
		panic(fmt.Sprintf("entry: nil Func for %q", name))
	}
	if _, dup := r.routes[name]; dup {
		panic(fmt.Sprintf("entry: duplicate entry %q", name))
	}
	if r.routes == nil {
		r.routes = make(map[string]Func)
	}
	r.routes[name] = fn
}

// Dispatch runs the entry named by args[0] with args[1:], where args is
// os.Args[1:]. With no matching entry it returns matched=false and runs
// nothing, so the caller continues into its normal main.
func (r *Router) Dispatch(args []string) (code int, matched bool) {
	if len(args) == 0 {
		return 0, false
	}
	fn, ok := r.routes[args[0]]
	if !ok {
		return 0, false
	}
	proc.setEntry(args[0])
	return fn(context.Background(), args[1:]), true
}
