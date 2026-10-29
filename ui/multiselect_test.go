package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dphaener/rexuikit/ui/theme"
)

func pluginField() *MultiSelect {
	return NewMultiSelect("plugins", "Plugins").Options(
		Option{"Claude Code", "claude"},
		Option{"Codex", "codex"},
		Option{"OpenCode", "opencode"},
	)
}

func gotStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestMultiSelectToggleAndDefault: space toggles the row under the cursor,
// movement does not, Default preselects, and Get stays in option order
// whatever order the choices were made in.
func TestMultiSelectToggleAndDefault(t *testing.T) {
	m := pluginField().Default("opencode")
	f := NewForm(theme.Dark(), colorprofile.TrueColor, m)
	gotStrings(t, m.Get(), []string{"opencode"})

	sendKeys(f, "down", "down", " ")
	gotStrings(t, m.Get(), nil2(t))
	sendKeys(f, "up", "up", "space")
	gotStrings(t, m.Get(), []string{"claude"})
	sendKeys(f, "down", "down", " ")
	gotStrings(t, m.Get(), []string{"claude", "opencode"})
	gotStrings(t, f.Values().Strings("plugins"), []string{"claude", "opencode"})

	// j and k move on an unfiltered list, like Select.
	sendKeys(f, "k", " ")
	gotStrings(t, m.Get(), []string{"claude", "codex", "opencode"})
}

// nil2 is the empty selection: Get returns a non-nil empty slice.
func nil2(t *testing.T) []string { t.Helper(); return []string{} }

// TestMultiSelectFilter: typing narrows the rows, space toggles inside the
// filtered list, and a choice survives the filter hiding its row.
func TestMultiSelectFilter(t *testing.T) {
	m := pluginField().Filterable()
	f := NewForm(theme.Dark(), colorprofile.TrueColor, m)
	cols, rows := f.NaturalSize()

	sendKeys(f, "d", "e", "x")
	if len(m.list.matches) != 1 {
		t.Fatalf("filter %q matched %d rows, want 1", m.list.query, len(m.list.matches))
	}
	sendKeys(f, " ")
	sendKeys(f, "z")
	if !strings.Contains(stripANSI(f.Render()), "no match") {
		t.Errorf("a filter matching nothing should show no match:\n%s", stripANSI(f.Render()))
	}
	sendKeys(f, " ")
	gotStrings(t, m.Get(), []string{"codex"})
	sendKeys(f, "backspace", "backspace", "backspace", "backspace")
	if len(m.list.matches) != 3 {
		t.Errorf("clearing the filter left %d rows, want 3", len(m.list.matches))
	}
	gotStrings(t, m.Get(), []string{"codex"})
	assertNaturalFrame(t, f.Render(), cols, rows)
}

// TestMultiSelectMin: below the minimum the form will not submit and the
// field shows the error; choosing enough clears it and enter submits.
// Load-bearing: the submitted Values go through Values.Strings.
func TestMultiSelectMin(t *testing.T) {
	m := pluginField().Min(1)
	f := NewForm(theme.Dark(), colorprofile.TrueColor, m)
	cols, rows := f.NaturalSize()

	if msgs := sendKeys(f, "enter"); len(msgs) != 0 {
		t.Fatalf("submitted with nothing chosen: %v", msgs)
	}
	if m.Err() == nil || !strings.Contains(stripANSI(f.Render()), "choose at least 1") {
		t.Errorf("no minimum error shown:\n%s", stripANSI(f.Render()))
	}
	assertNaturalFrame(t, f.Render(), cols, rows)

	sendKeys(f, " ")
	if m.Err() != nil {
		t.Errorf("error outlived the toggle that fixed it: %v", m.Err())
	}
	msgs := sendKeys(f, "enter")
	if len(msgs) != 1 {
		t.Fatalf("enter produced %v, want one submit", msgs)
	}
	done, ok := msgs[0].(FormSubmittedMsg)
	if !ok {
		t.Fatalf("enter produced %T, want FormSubmittedMsg", msgs[0])
	}
	gotStrings(t, done.Values.Strings("plugins"), []string{"claude"})

	// Unchecking back under the minimum is caught again at validation.
	m2 := pluginField().Min(2).Default("claude")
	if m2.validate() == nil {
		t.Error("one chosen passed Min(2)")
	}
	if NewMultiSelect("k", "K").validate() != nil {
		t.Error("an empty field with no minimum should validate")
	}
}

// TestMultiSelectOnChange: the form's value comparison must not panic and
// the hook fires only for a toggle, not for a cursor move.
func TestMultiSelectOnChange(t *testing.T) {
	var seen []any
	m := pluginField().OnChange(func(v any) tea.Cmd { seen = append(seen, v); return nil })
	f := NewForm(theme.Dark(), colorprofile.TrueColor, m)
	sendKeys(f, "down", " ", "down", "up")
	if len(seen) != 1 || seen[0] != "codex" {
		t.Errorf("OnChange saw %q, want one call with codex", seen)
	}
	if m.Value() != "codex" {
		t.Errorf("Value = %v", m.Value())
	}
}

// TestMultiSelectRowsScroll: a fixed row count scrolls to keep the cursor
// visible and the field's height does not change.
func TestMultiSelectRowsScroll(t *testing.T) {
	var opts []Option
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		opts = append(opts, Option{v, v})
	}
	m := NewMultiSelect("k", "K").Options(opts...).Rows(2)
	f := NewForm(theme.Dark(), colorprofile.TrueColor, m)
	cols, rows := f.NaturalSize()
	sendKeys(f, "down", "down", "down", " ")
	gotStrings(t, m.Get(), []string{"d"})
	plain := stripANSI(f.Render())
	if !strings.Contains(plain, "[x] d") || strings.Contains(plain, "[ ] a") {
		t.Errorf("window did not scroll to d:\n%s", plain)
	}
	assertNaturalFrame(t, f.Render(), cols, rows)
}

// TestMultiSelectPaint: every cell of the frame has an explicit background,
// in both themes, focused and not, with rows chosen and a minimum error.
func TestMultiSelectPaint(t *testing.T) {
	for _, th := range []struct {
		name string
		t    theme.Theme
	}{{"dark", theme.Dark()}, {"light", theme.Light()}} {
		t.Run(th.name, func(t *testing.T) {
			m := pluginField().Filterable().Min(2).Default("codex")
			other := NewText("n", "Name")
			f := NewForm(th.t, colorprofile.TrueColor, m, other)
			cols, rows := f.NaturalSize()
			assertNaturalFrame(t, f.Render(), cols, rows)
			sendKeys(f, "enter") // focus moves on only when valid; Min(2) blocks it
			assertNaturalFrame(t, f.Render(), cols, rows)
			sendKeys(f, "down", " ")
			sendKeys(f, "enter")
			assertNaturalFrame(t, f.Render(), cols, rows)
		})
	}
}
