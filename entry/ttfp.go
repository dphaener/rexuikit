package entry

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// EnvTTFP names a file that receives one line per run: entry=<name> ttfp_ms=<n>.
const EnvTTFP = "REXUIKIT_TTFP"

// processStart is taken at package init, after the Go runtime has started, so
// the figure runs low by that startup cost.
var processStart = time.Now()

type paint struct {
	start time.Time
	done  chan struct{}
	once  sync.Once

	mu    sync.Mutex
	entry string
}

func newPaint(start time.Time) *paint {
	return &paint{start: start, done: make(chan struct{}), entry: "main"}
}

var proc = newPaint(processStart)

func (p *paint) setEntry(name string) {
	p.mu.Lock()
	p.entry = name
	p.mu.Unlock()
}

func (p *paint) entryName() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry
}

// markPainted writes the TTFP line before closing done, so deferred work never
// starts ahead of its own measurement. A failed write is dropped: the
// instrumentation must not fail the program it measures.
func (p *paint) markPainted(now time.Time, ttfpFile string) {
	p.once.Do(func() {
		if ttfpFile != "" {
			_ = appendLine(ttfpFile, formatTTFP(p.entryName(), now.Sub(p.start)))
		}
		close(p.done)
	})
}

func (p *paint) painted() <-chan struct{} {
	return p.done
}

// deferred gives each call its own goroutine that waits on done. A call made
// after paint finds done already closed and runs at once.
func (p *paint) deferred(fn func()) {
	go func() {
		<-p.done
		fn()
	}()
}

// MarkPainted reports the first non-empty frame. Call it once after the first
// render; later calls are no-ops.
func MarkPainted() {
	proc.markPainted(time.Now(), os.Getenv(EnvTTFP))
}

// Painted returns a channel that the first MarkPainted closes. A bubbletea
// program wraps it in a tea.Cmd, which keeps bubbletea out of this package.
func Painted() <-chan struct{} {
	return proc.painted()
}

// Deferred runs fn once after the first MarkPainted. A process that exits
// before paint drops it without waiting.
func Deferred(fn func()) {
	proc.deferred(fn)
}

func formatTTFP(entry string, elapsed time.Duration) string {
	return fmt.Sprintf("entry=%s ttfp_ms=%d\n", entry, elapsed.Milliseconds())
}

// appendLine uses O_APPEND so concurrent runs each add whole lines.
func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
