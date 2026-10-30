# Testing

## Hermetic tests

```sh
make test
make vet
gofmt -l .
```

`client/clienttest` is an in-process fake Rex server over TCP loopback, so it runs in the sandbox:

```go
srv := clienttest.New(t)
srv.Reply("session.attach", map[string]any{"session_id": "s1"})
c, err := client.Dial(ctx, client.Options{Addr: srv.URL, HTTPClient: srv.HTTPClient()})
```

Use `srv.Handle` to inspect a request. `clienttest.CallerFrom(ctx)` tells you which connection made it.

## Frames

Assert that every cell has a background:

```go
if n := paint.Holes(frame, w, h); n != 0 {
	t.Fatalf("%d cells have no background", n)
}
```

Golden frames: `go test ./ui/ -update` rewrites `ui/testdata/golden`. Review the diff, because it is the layout review.

## Live tests

```sh
make live
```

These run against a real Rex server. Run them outside the sandbox, from a shell inside a Rex session, with the Rex app showing that session.

- They open layers over the window holding `$REX_BLOCK` and close everything they open.
- A block the app never sizes fails with `NO_APP_RESIZE`. Bring the app to the front and run again.
- `TestPerformSessionSelect` and `TestConfigReload` change your own session and config.
- Run after every Rex upgrade. A failure there is a Rex change, not a rexuikit bug.
