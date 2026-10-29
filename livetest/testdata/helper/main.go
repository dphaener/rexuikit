//go:build darwin

// Command helper is the program the live contract tests run inside Rex
// blocks. It reports what the block's terminal looks like from the inside,
// which the server's own API cannot show: the size the program sees through
// TIOCGWINSZ, and the exact bytes it receives on stdin (focus reports
// included).
//
//	helper -out /tmp/rexuikit-live.../report.json
//
// The report is a JSON object rewritten atomically (temp file + rename)
// whenever something changes, so a test polling it never reads a torn file.
// The helper turns on terminal focus reporting (DECSET 1004) and puts the
// terminal in non-canonical mode: in canonical mode a focus report, which
// carries no newline, would sit in the line discipline and never arrive.
//
// It lives under testdata so `go build ./...` and `go vet ./...` skip it;
// the tests build it explicitly with `go build ./testdata/helper`. It is
// darwin-only because the termios ioctls it uses (TIOCGETA/TIOCSETA) are
// the BSD names, and Rex runs on macOS.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Report is the file the tests read. Field names are the contract with
// live_test.go's helperReport.
type Report struct {
	PID      int    `json:"pid"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Input    string `json:"input"`
	FocusIn  int    `json:"focus_in"`
	FocusOut int    `json:"focus_out"`
}

const (
	focusIn  = "\x1b[I"
	focusOut = "\x1b[O"
)

type winsize struct {
	Row, Col, X, Y uint16
}

func termSize(fd uintptr) (cols, rows int, err error) {
	var ws winsize
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); e != 0 {
		return 0, 0, e
	}
	return int(ws.Col), int(ws.Row), nil
}

// rawInput clears ICANON and ECHO so every byte, including a bare focus
// report, is readable as soon as it arrives and is not echoed onto the
// screen the tests may read with terminal.format.
func rawInput(fd uintptr) error {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	t.Lflag &^= syscall.ICANON | syscall.ECHO
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

type reporter struct {
	mu   sync.Mutex
	out  string
	rep  Report
	last string
}

// flush writes the report when it differs from the last one written. The
// rename is atomic within one directory, which is why the temp file sits
// beside the report.
func (r *reporter) flush() {
	r.rep.FocusIn = strings.Count(r.rep.Input, focusIn)
	r.rep.FocusOut = strings.Count(r.rep.Input, focusOut)
	b, err := json.Marshal(r.rep)
	if err != nil || string(b) == r.last {
		return
	}
	tmp := r.out + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil || os.Rename(tmp, r.out) != nil {
		return
	}
	r.last = string(b)
}

func main() {
	out := flag.String("out", "", "report file to rewrite atomically")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "helper: -out is required")
		os.Exit(2)
	}
	in := os.Stdin.Fd()
	if err := rawInput(in); err != nil {
		fmt.Fprintln(os.Stderr, "helper: termios:", err)
		os.Exit(1)
	}
	// Focus reporting on, then a marker the tests can wait for on screen.
	fmt.Print("\x1b[?1004h")
	fmt.Print("rexuikit-livetest helper ready\r\n")

	r := &reporter{out: *out, rep: Report{PID: os.Getpid()}}
	sample := func() {
		cols, rows, err := termSize(in)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.rep.Cols, r.rep.Rows = cols, rows
		r.flush()
		r.mu.Unlock()
	}
	sample()

	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.rep.Input += string(buf[:n])
				r.flush()
				r.mu.Unlock()
			}
			if err != nil {
				os.Exit(0)
			}
		}
	}()

	// Polling the size instead of waiting for SIGWINCH keeps the helper to
	// one code path; 20ms is far below the tests' own poll deadlines.
	for {
		time.Sleep(20 * time.Millisecond)
		sample()
	}
}
