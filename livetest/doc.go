//go:build rexlive

// Package livetest holds rexuikit's live contract tests: one test per Rex
// behavior that rexuikit relies on but the server's spec does not promise,
// each re-proving an entry of docs/rex-behaviors.md against the real server.
// Rex is a 0.0.x beta, so these tests are how a Rex upgrade that changes a
// relied-on behavior gets caught before rexuikit or forge silently breaks.
//
// Every file is behind the rexlive build tag, so the default `go test ./...`
// never compiles them. Run them unsandboxed (the sandbox blocks the Rex
// socket) from a shell inside a Rex session, with the Rex app showing it:
//
//	cd rexuikit && go test -tags rexlive -count=1 -v ./livetest/...
//
// The suite talks to Rex through rexuikit/client. The tests and their helpers
// may also shell out to the rex CLI (TestLuaCanWriteFile runs `rex do`); the
// no-CLI rule binds rexuikit's library code, not its tests.
//
// Isolation: the suite opens its floating layers over the window the user
// is already looking at, the one holding $REX_BLOCK, mostly in its right
// half, and closes every layer and block it makes through t.Cleanup. It
// never creates a window: the Rex app only renders, and so only sizes, the
// window on screen, and a new window is not on screen until the user
// switches to it. It never closes, splits, rearranges or writes to the
// user's pane; it reads the pane's size (the window's cell size) and, at the
// end, gives focus back to the block that had it. Every block it creates is
// labeled rexuikit-livetest*, and TestMain closes any such block left by a
// crashed run, at start and again at the end.
//
// Sizing and ownership: the connection that creates a block owns it, and
// while a non-app client owns a block the Rex app's size is not applied.
// Tests that need the app to size a block (TestLayerChromeDelta, the
// final-size step of TestPrewarmPlacement) call session.disown_block on it,
// wait until an app client owns it and its size holds, then assert. They
// skip with a message when no app client is attached to the session, and
// fail with NO_APP_RESIZE (owner, desired sizes and size) when an app is
// attached but never claims the block: the app is not showing this window,
// so bring the Rex app to the front on this tab and rerun.
//
// Exceptions to "never creates a window": the tests for forge's session and
// window wrappers (CreateSession, FocusWindow, SetWindowLabel, BlockProcess,
// AttachAll) run every mutating call inside ONE throwaway session labeled
// rexuikit-livetest-session, which each test destroys in t.Cleanup and
// TestMain sweeps at start and end. Before session.destroy the suite
// attaches live.c to the session, because the server refuses the call
// otherwise; the sweep does the same. TestAttachAllDeliversBlockEvents
// dials its own connection, closed in cleanup, so attach_all's event scope
// never reaches live.c or its hub. Two tests touch the user's own state by
// design: TestPerformSessionSelect moves the Rex app to the throwaway
// session and selects the user's original session and window again in
// t.Cleanup, before the session is destroyed, so later tests still find the
// app showing $REX_BLOCK's window; TestConfigReload reloads the user's real
// config (a successful reload with diagnostics still passes).
//
// Programs inside blocks are testdata/helper, built once per run into a
// mkdtemp directory under /tmp, which reports the terminal size it sees and
// every byte it receives to a JSON file there.
package livetest
