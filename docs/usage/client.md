# Client

`client` is one WebSocket connection to Rex. Calls are multiplexed over it and events arrive on one channel.

## Connect

```go
c, err := client.Dial(ctx, client.Options{Name: "my-tool", Version: "0.1.0"})
if err != nil {
	return err
}
defer c.Close()
```

With no `Addr`, `Dial` reads `REX_SERVER`. Rex sets `REX_SESSION` and `REX_BLOCK` for programs it runs.

## Call a method

```go
sessions, err := c.ListSessions(ctx)
_, err = c.FocusBlock(ctx, sessionID, blockID)
```

Typed methods are in `client/methods.go`. For anything without a wrapper, use `c.Call(ctx, method, payload, &out)`.

## Events

```go
for ev := range c.Events() {
	if se, ok, err := ev.Session(); ok && err == nil {
		// handle se.Type
	}
}
```

Drain `Events()`. If nothing reads it, the connection fails with `ErrEventOverflow`. For a connection that only makes calls, set `Options.DisableEvents`.

## Errors

```go
if errors.Is(err, client.ErrNotFound) {
	// the session, window or block is gone
}
```

After `Close`, calls return `client.ErrClosed`. Reconnecting is not implemented.
