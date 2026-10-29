package entry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestDispatch: the "no match" rows must report matched=false without calling
// any Func, because main relies on that fall-through to reach its normal path.
// The "remaining args" row must hand over args[1:] exactly, since boot
// functions parse their own flags. "opened" guards whole-word matching.
func TestDispatch(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantMatched bool
		wantCode    int
		wantArgs    []string
		wantCalled  bool
	}{
		{name: "empty args fall through", args: nil},
		{name: "unknown entry falls through", args: []string{"nope", "x"}},
		{name: "entry must be first word", args: []string{"x", "open"}},
		{name: "prefix of entry does not match", args: []string{"opened"}},
		{
			name:        "matched with no remaining args",
			args:        []string{"open"},
			wantMatched: true,
			wantCode:    7,
			wantArgs:    []string{},
			wantCalled:  true,
		},
		{
			name:        "matched receives remaining args",
			args:        []string{"open", "--session", "a b", "-"},
			wantMatched: true,
			wantCode:    7,
			wantArgs:    []string{"--session", "a b", "-"},
			wantCalled:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Router
			called := false
			var gotArgs []string
			r.Handle("open", func(_ context.Context, args []string) int {
				called = true
				gotArgs = args
				return 7
			})
			code, matched := r.Dispatch(c.args)
			if matched != c.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, c.wantMatched)
			}
			if called != c.wantCalled {
				t.Fatalf("called = %v, want %v", called, c.wantCalled)
			}
			if code != c.wantCode {
				t.Fatalf("code = %d, want %d", code, c.wantCode)
			}
			if c.wantCalled && !slices.Equal(gotArgs, c.wantArgs) {
				t.Fatalf("args = %q, want %q", gotArgs, c.wantArgs)
			}
		})
	}
}

// TestHandleRejects: the invalid-name rows protect the TTFP line format, and
// the duplicate row stops a silent shadow of one fast path by another. Each row
// must panic, since a registration error is a startup bug.
func TestHandleRejects(t *testing.T) {
	ok := func(context.Context, []string) int { return 0 }
	cases := []struct {
		name  string
		entry string
		fn    Func
		prior bool
	}{
		{name: "empty name", entry: "", fn: ok},
		{name: "name with space", entry: "new session", fn: ok},
		{name: "name with equals", entry: "a=b", fn: ok},
		{name: "nil func", entry: "new", fn: nil},
		{name: "duplicate name", entry: "dup", fn: ok, prior: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Router
			if c.prior {
				r.Handle(c.entry, ok)
			}
			defer func() {
				if recover() == nil {
					t.Fatalf("Handle(%q) did not panic", c.entry)
				}
			}()
			r.Handle(c.entry, c.fn)
		})
	}
}

func TestFormatTTFP(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		elapsed time.Duration
		want    string
	}{
		{name: "zero", entry: "main", elapsed: 0, want: "entry=main ttfp_ms=0\n"},
		{name: "truncates to whole ms", entry: "new", elapsed: 85900 * time.Microsecond, want: "entry=new ttfp_ms=85\n"},
		{name: "seconds", entry: "oneoff", elapsed: 1200 * time.Millisecond, want: "entry=oneoff ttfp_ms=1200\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatTTFP(c.entry, c.elapsed); got != c.want {
				t.Fatalf("formatTTFP = %q, want %q", got, c.want)
			}
		})
	}
}

// TestMarkPaintedWritesOneLine: the second markPainted must write nothing, even
// with a different file, because the first paint is the only one measured. A
// run therefore yields exactly one well-formed line.
func TestMarkPaintedWritesOneLine(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")

	start := time.Now()
	p := newPaint(start)
	p.setEntry("new")
	p.markPainted(start.Add(90*time.Millisecond), first)
	p.markPainted(start.Add(500*time.Millisecond), second)

	got, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read ttfp file: %v", err)
	}
	if want := "entry=new ttfp_ms=90\n"; string(got) != want {
		t.Fatalf("ttfp file = %q, want %q", got, want)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("second markPainted wrote a file: stat err = %v", err)
	}
}

func TestMarkPaintedUnsetWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p := newPaint(time.Now())
	p.markPainted(time.Now(), "")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unset TTFP wrote %d files", len(entries))
	}
	select {
	case <-p.painted():
	default:
		t.Fatal("painted channel not closed after markPainted")
	}
}

// TestDeferredRunsOnceAfterPaint: the first wait proves deferred work is held
// until paint. Without it, a Deferred that ran at registration would pass the
// later checks. The second markPainted proves exactly-once even when paint is
// signalled again.
func TestDeferredRunsOnceAfterPaint(t *testing.T) {
	p := newPaint(time.Now())
	ran := make(chan int, 4)
	p.deferred(func() { ran <- 1 })
	p.deferred(func() { ran <- 2 })

	select {
	case v := <-ran:
		t.Fatalf("deferred %d ran before paint", v)
	case <-time.After(20 * time.Millisecond):
	}

	p.markPainted(time.Now(), "")
	seen := map[int]int{}
	for range 2 {
		select {
		case v := <-ran:
			seen[v]++
		case <-time.After(2 * time.Second):
			t.Fatal("deferred work did not run after paint")
		}
	}
	if seen[1] != 1 || seen[2] != 1 {
		t.Fatalf("deferred runs = %v, want each once", seen)
	}

	p.markPainted(time.Now(), "")
	select {
	case v := <-ran:
		t.Fatalf("deferred %d ran again on second paint", v)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDeferredAfterPaintRunsImmediately(t *testing.T) {
	p := newPaint(time.Now())
	p.markPainted(time.Now(), "")
	ran := make(chan struct{}, 1)
	p.deferred(func() { ran <- struct{}{} })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("deferred registered after paint never ran")
	}
}
