# rexuikit

rexuikit is a Go toolkit for building Rex floating layers. It has a typed client
for Rex's control protocol, surfaces that open, size and place layers, a
fast-path entry router, and a native content kit.

It is a standalone Go module, `github.com/dphaener/rexuikit`. For a Rex-facing
component, make every Rex call through `client`. Don't shell out to the `rex`
CLI.

## Packages

- `client`: one WebSocket connection to the Rex server. Requests are
  multiplexed over it and events arrive on one channel. `methods.go` holds the
  typed wrappers: layers, blocks and windows (`NewWindow`, `CloseWindow`,
  `FocusWindow`, `SetWindowLabel`), sessions (`CreateSession`,
  `DestroySession`, `ListSessions`, `View`), `AttachAll` with an `EventScope`
  of `ScopeSession` or `ScopeAll`, `ListClients`, `Perform` for
  `client.perform`, `ConfigReload`, and `BlockProcess` for the terminal
  `process` method, whose `.Child.PID` is the program under `Shell: "none"`.
  `client/clienttest` is the in-process fake server. It serves every
  connection, `Emit` reaches all of them, `EmitAttachAll` only the ones that
  called `session.attach_all`, and a handler reads its caller through
  `CallerFrom`. The protocol itself is not restated here. Read
  [`docs/rex-api/llms-r7.md`](docs/rex-api/llms-r7.md).
- `surface`: opens a `Modal`, `Drawer` or `Split` layer, sizes it in cells,
  animates it, focuses it and closes it. `Surface.Warm` starts a detached,
  already-drawn block for the pre-warm pool, `WarmLabel`, `IsWarm` and
  `FindWarm` index those blocks, and `Surface.Place` puts one in a layer and
  relabels it with the title you pass, which Rex shows in the layer's title
  bar. The plan had a fixed `forge-live:<action>` label (`LiveLabel`) here.
  The user chose the form's title instead, so there is no `LiveLabel`.
  `surface/program` is the in-program half: it dedupes focus reports for a
  bubbletea program running inside a surface.
- `ui`: the content kit. `Card`, `Dialog`, `Form` (with `Text`, `TextArea`,
  `Select`, `MultiSelect` and `Confirm` fields), `List` and `InfoPanel`, plus
  the dashboard's building blocks: `Pane` (a titled, bordered panel), `Tabs`
  (a tab strip with one active label), `HintBar` (a status plus key hints on
  one row) and `Notice` (a one-line toast in the Info, Success, Warning or
  Error colour, with no timer of its own). A `MultiSelect` field's value is a
  `[]string`, so read it with `Values.Strings` rather than comparing it. Every rendered cell
  carries an explicit background. `ui/paint` fills the cells that have none
  and counts the ones left over. `ui/theme` holds the semantic colour tokens,
  light and dark. `Form`, `Dialog`, `List` and `InfoPanel` ask for the
  terminal's background in `Init` and re-theme from the reply. `Card` is
  drawn by them and has no `Init`.
- `entry`: fast-path dispatch and time to first paint. `MarkPainted` records
  the first frame, `Deferred` queues work to run after it, and `Painted`
  reports it. TTFP is written to the file named by `REXUIKIT_TTFP`.
- `livetest`: live contract tests, behind the `rexlive` build tag.

forge uses `entry.MarkPainted`, `entry.Deferred` and `entry.Painted`. It does
not use `entry.Router`. The quick-action entry point orders its own work.

## Model

Each surface opens over a Rex control connection. Opening one creates a block
and a layer, disowns the block so the Rex app sizes it, and returns a handle.
Rex gives a layer 80x24 unless the open passes `size`. So `surface` works out
the usable cell size from the window and the chrome constants in
[`docs/rex-behaviors.md`](docs/rex-behaviors.md), then passes it.

The packages depend on narrow interfaces. `surface.Rex` is the part of
`*client.Client` that `surface` uses, and `ui` components take a theme and a
colour profile. Tests run against fakes.

## Fast-path rules

These rules are what the quick actions follow. A new entry point should follow
them too.

1. Paint first. Call `entry.MarkPainted` on the first frame, once per process.
   Queue everything else with `entry.Deferred`, and it runs after the paint.
2. Keep pre-paint work small. `new` and `oneoff` may run one `git rev-parse`
   and one `.forge/` read before their first frame, because their name
   validators need the taken names. Their base branches load inside the form,
   after the paint. `quickBootWork` in `quick.go` is the table that tests this.
3. No `rex` CLI calls, on a fast path or off it. A CLI call costs about 76 ms.
   A call on the open connection costs 0.1 to 3 ms.
4. A warm run does not start the Rex watcher. Its `REX_*` environment belongs
   to the Rex server, not the terminal, so its session, prewarm directory and
   server URL come in as flags.

## Testing

- Unit tests: `make -C rexuikit test`, or `cd rexuikit && go test ./...`. They are
  hermetic and pass in the sandbox. The fake in `client/clienttest` serves over
  TCP loopback, because the sandbox denies unix-socket `bind()`.
- Vet: `make -C rexuikit vet`.
- Testing a component: drive it with key messages and assert on the rendered
  frame. Run every geometry through `paint.Holes(frame, w, h)` and expect 0,
  since a cell with no background is a bug. lipgloss v2 always emits truecolor
  SGR, so a test can assert a colour by substring. `Form` compares a field's
  `Value()` with `!=` on `any` after each key, so a field's `Value()` must be
  comparable (a string or bool, never a slice).
- Testing a client call: register a handler on `clienttest.Server`. A handler
  can see which connection called and what it attached, so a test can fail
  `session.disown_block` on a connection that never attached. A test that
  needs `attach_all` events uses `EmitAttachAll`.
- Live tests: `make -C rexuikit live`, which runs
  `go test -tags rexlive ./livetest/...`. They talk to the real Rex server, so
  run them unsandboxed, from a shell inside a Rex session, with the Rex app
  showing that session's window. A block the app never sizes fails with
  `NO_APP_RESIZE`. Bring the app to the front and run again. The suite opens
  layers over the window that holds `$REX_BLOCK` and closes everything it
  made. It never creates a window in your session, with one exception: the
  tests for the session and window wrappers (`CreateSession`, `FocusWindow`,
  `SetWindowLabel`, `BlockProcess`, `AttachAll`) work inside one throwaway
  session labeled `rexuikit-livetest-session`, destroyed in cleanup and swept
  by `TestMain` at start and end. Two tests touch your own state on purpose:
  `TestPerformSessionSelect` moves the Rex app to the throwaway session and
  selects your session again before cleanup, and `TestConfigReload` reloads
  your real config. Each contract test re-proves a row of
  [`docs/rex-behaviors.md`](docs/rex-behaviors.md).
- Root `make check` runs fmt-check, vet and test for both modules, the same
  steps CI runs. Use it, not only `make -C rexuikit test`: the root build
  compiles rexuikit through its `replace`, so a rexuikit change can break it.

## Refreshing the API snapshot

Run `rexuikit/scripts/rex-api-snapshot.sh` against the running server, unsandboxed.
It needs `curl`, `jq` and the `rex` CLI, and it reads `REX_SERVER` (a
`unix://` URL with a percent-encoded path) or falls back to the default macOS
socket. It writes three files into `docs/rex-api/`:

- `llms-r<rev>.md` from `/llms.txt`
- `api-help-r<rev>.json` from `/api/help`, with sorted keys
- `VERSION`, which has no run date, so an unchanged server gives identical files

`<rev>` is the API revision. A new revision adds a new pair beside the old one,
and the script never deletes the old pair. Remove it by hand once the new one
is in place.

## Re-measuring layer chrome

Rex draws a border and a title around each layer. Those cost cells, and the
cost depends on where the layer sits. The constants are in
[`docs/rex-behaviors.md`](docs/rex-behaviors.md) under `## Layer chrome`, and
`TestLayerChromeDelta` reads them from that section.

To re-measure, bring the window into view in the Rex app, then run:

```
rex do -s "$REX_SESSION" rexuikit/scripts/measure-chrome.lua wid=window:<uuid> block=block:<uuid>
```

`wid` is the window to open layers over. `block` is that window's lone tiled
block. The script exits 2 with `NO_APP_RESIZE` if the app never sizes the
block. Add `fit=0` to print the samples only and fit them offline; the search
is slow in Rex's Lua. The script also splits a block inside one of its own
layers to measure how far off a split window's size is. Update the constants
in `## Layer chrome` from the output, then run `make -C rexuikit live`.

`rexuikit/scripts/measure-ttfp.lua` times a layer from open until a marker is
on screen. It is the method behind the TTFP numbers.

## Consumers

Applications import this module by tagged version. Keep application policy in
the application: which actions warm, generated Lua, and state paths. `surface`
exports the warm label prefix (`WarmPrefix`) because it identifies its own warm
blocks. Where an application keeps its pre-warm files is the application's
choice.

Rex is a 0.0.x beta. The live suite is the contract. Run it after every Rex
upgrade, and treat a failure as a Rex change, not a rexuikit bug.
