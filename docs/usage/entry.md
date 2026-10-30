# Entry

`entry` lets a quick action boot and paint before the rest of the program initializes.

## Dispatch

```go
var r entry.Router

func init() {
	r.Handle("quick-new", func(ctx context.Context, args []string) int {
		return runNew(ctx, args)
	})
}

func main() {
	if code, ok := r.Dispatch(os.Args[1:]); ok {
		os.Exit(code)
	}
	// normal startup
}
```

`Dispatch` runs nothing when no entry matches, so `main` continues.

## Paint, then defer

```go
entry.MarkPainted() // once, on the first non-empty frame
entry.Deferred(func() {
	loadBranches()
})
```

`Deferred` runs after the first paint. `entry.Painted()` returns a channel closed by the first `MarkPainted`. Wrap it in a `tea.Cmd` to keep Bubble Tea out of this package.

## Time to first paint

Set `REXUIKIT_TTFP` to a file path. `MarkPainted` appends `entry=<name> ttfp_ms=<n>` to it.

## Rules

- Keep work before the first paint small.
- Make no `rex` CLI calls.
