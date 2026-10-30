# Forms

`ui.NewForm` builds a Bubble Tea model from fields. The form doesn't quit the program. The host decides what submit and cancel mean.

## Build

```go
form := ui.NewForm(theme.Dark(), colorprofile.TrueColor,
	ui.NewText("name", "Name").Placeholder("my-branch").Validator(validName),
	ui.NewSelect("base", "Base").Options(ui.Option{Label: "main", Value: "main"}).Default("main"),
	ui.NewMultiSelect("labels", "Labels").Options(ui.Option{Label: "bug", Value: "bug"}),
	ui.NewConfirm("push", "Push now").Default(true),
).WithSubtitle("New branch")
```

Fields: `Text`, `TextArea`, `Select`, `MultiSelect`, `Confirm`. `Select` takes `Loader` for async options and `Filterable()` and `Rows(n)` for layout.

## Read the result

```go
case ui.FormSubmittedMsg:
	name := msg.Values.String("name")
	push := msg.Values.Bool("push")
	labels := msg.Values.Strings("labels")
case ui.FormCancelledMsg:
	return m, tea.Quit
```

`form.Status()` returns `ui.FormPending`, `ui.FormSubmitted` or `ui.FormCancelled`.

## Gotchas

- A field's `Value()` must be comparable, because the form compares values with `!=`. Read multi-selects with `Values.Strings`.
- While a `Select` loads, it reserves 4 rows unless `Rows` is set.
