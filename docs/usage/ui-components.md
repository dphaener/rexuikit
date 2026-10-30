# UI components

Each component renders to a string at a given size. Use `theme.Dark()`, `theme.Light()` or `theme.FromBackground(bg)`, and pass a `colorprofile.Profile`.

## List

```go
l := ui.NewList(th, profile, []ui.Row{{Label: "main", Detail: "default"}}).WithTitle("Branches")
```

It sends `ui.ListSelectedMsg{Index, Row}` on enter and `ui.ListCancelledMsg{}` on esc. `l.Selected()` returns the current row.

## Dialog

```go
d := ui.NewDialog(th, profile, "Delete?", []string{"This can't be undone."}).WithHint("y yes · n no")
```

Its `Update` returns `tea.Cmd`, not a model. Call it from the host's `Update`.

## Info panel

```go
p := ui.NewInfoPanel(th, profile, "Status", func(width int) []string {
	return statusLines(width)
}).WithRefresh(time.Second)
```

It scrolls and sends `ui.InfoClosedMsg{}` on esc or q.

## Pane, tabs, hints, notices

```go
pane := ui.NewPane(th, profile).WithTitle("Log").WithFocus(true).Render(body, w, h)
tabs := ui.NewTabs(th, profile, []string{"Logs", "Diff"}, 0).Render(w)
bar := ui.NewHintBar(th).WithStatus("ready").Render([]ui.Hint{{Key: "enter", Desc: "select"}}, w)
note := ui.Notice{Kind: ui.Warning, Text: "rebase needed"}.Render(th, w)
```

Notice kinds are `ui.Info`, `ui.Success`, `ui.Warning` and `ui.Error`. A notice has no timer, so the caller decides when to stop showing it.

## Card and cells

`ui.NewCard(th, profile).WithSubtitle(s).Render(content, w, h)` draws a padded frame with no border. `paint.Fill(frame, bg, profile)` fills any cell that lacks a background.
