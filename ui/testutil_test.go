package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dphaener/rexuikit/ui/paint"
)

// updateGolden rewrites every golden frame from the current rendering instead
// of comparing: `go test ./ui/ -update`. The resulting git diff of
// testdata/golden is the review artifact for a layout change.
var updateGolden = flag.Bool("update", false, "rewrite golden frames under testdata/golden")

// stripANSI removes escape sequences, leaving the characters a user sees.
func stripANSI(s string) string { return xansi.Strip(s) }

// assertGolden compares one frame against testdata/golden/<name>.txt. The
// frame is ANSI-stripped, so the file holds characters only; colour is
// asserted by the zero-holes check and the theme's contrast tests. Trailing
// spaces are kept (they are cells), line endings are normalised, and a
// trailing newline is added so editors and git diff treat the file as text.
func assertGolden(t *testing.T, name, frame string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	got := strings.ReplaceAll(stripANSI(frame), "\r\n", "\n") + "\n"
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with -update to create it)", path, err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s (run with -update to accept):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// assertNoHoles fails if any cell of frame, on a w×h canvas, has no
// background: a see-through cell inside a Rex layer.
func assertNoHoles(t *testing.T, frame string, w, h int) {
	t.Helper()
	if n := paint.Holes(frame, w, h); n != 0 {
		t.Errorf("%d cell(s) of the %dx%d frame have no background:\n%s", n, w, h, stripANSI(frame))
	}
}
