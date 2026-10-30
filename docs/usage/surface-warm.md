# Warm surfaces

A warm surface starts its program in a detached block at the right size, so the first frame is ready when it's placed. Use it when cold start is too slow for a quick action.

## Warm

Label the block with `surface.WarmLabel(action, buildID)`. The label ties it to one build of the binary, so a rebuilt program never shows a stale frame.

```go
spec := surface.Spec{
	Kind:  surface.Modal,
	Argv:  argv,
	Label: surface.WarmLabel("new-branch", buildID),
	Cells: client.Size{Columns: 60, Rows: 20},
}
wb, err := s.Warm(ctx, spec)
```

## Place

Pass the same bounds the spec resolves to, and a non-empty title that doesn't start with `surface.WarmPrefix`:

```go
h, err := s.Place(ctx, wb, bounds, "New branch", true)
```

`Place` relabels the block to the title, so the warm label is dropped and the placed block is never found again.

## Find a warm block

```go
blocks, err := c.ListBlocks(ctx, sessionID)
if b, ok := surface.FindWarm(blocks, "new-branch"); ok {
	// b.BlockID is ready to place
}
```

`surface.IsWarm` checks one block. `FindWarm` returns the oldest detached block for the action.
