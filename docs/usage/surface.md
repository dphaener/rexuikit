# Surfaces

`surface` opens a program as a layer in a Rex session. It computes the usable size, animates drawers, and tracks when the surface is gone.

## Attach and create

The connection must be attached to the session first. `surface` reads the connection's events itself, so don't read `Events()` alongside it.

```go
if _, err := c.Attach(ctx, sessionID); err != nil {
	return err
}
s := surface.New(c, sessionID)
```

## Open a modal

```go
h, err := s.Open(ctx, surface.Spec{
	Kind:  surface.Modal,
	Argv:  []string{"my-tool", "picker"},
	Label: "Pick a branch",
	Cells: client.Size{Columns: 60, Rows: 20},
	Focus: true,
	Blur:  surface.CloseOnBlur,
})
if err != nil {
	return err
}
<-h.Done()
```

Set `Cells` for a content-sized layer, or `Bounds` as window fractions `[x, y, w, h]`. Either one is required for a modal or drawer.

## Drawer

Use `Kind: surface.Drawer` and `Edge: surface.EdgeRight` (or `EdgeLeft`, `EdgeTop`, `EdgeBottom`). It slides in on open and out on `Close`.

## Split

```go
h, err := s.Open(ctx, surface.Spec{
	Kind:        surface.Split,
	Argv:        []string{"my-tool", "log"},
	AnchorBlock: blockID,
	Direction:   client.Horizontal,
	Side:        client.After,
	Ratio:       0.4,
})
```

A split needs `AnchorBlock` and ignores `Cells`.

## Handle

```go
h.Resize(ctx, client.Bounds{0.5, 0, 0.5, 1}) // layers only
h.Raise(ctx)                                 // layers only
h.Close(ctx)
```

`Resize` and `Raise` return `surface.ErrNotLayer` on a split. `h.Done()` closes when the block exits, the layer is removed, or the connection ends.
