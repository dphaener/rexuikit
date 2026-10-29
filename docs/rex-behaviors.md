# Rex behaviors rexuikit relies on

Behaviors the Rex API spec (`rex-api/llms-r7.md`) does not state, each with
the evidence, the Rex version observed, and the live test that re-proves it
(`rexuikit/livetest`, build tag `rexlive`, written in W3.T3).

- **Rex observed**: `0.0.1792787366-g623b2d`, commit `623b2d17`, api revision 7
  (`rex-api/VERSION`). Rex is a 0.0.x beta: re-run the live tests after any
  Rex upgrade and treat a failure as a Rex change, not a rexuikit bug.
- **Confidence**: `observed` = measured in a run recorded here or in the
  `.forge/rex-tui-components/learnings.md` notepad. `inferred` = seen once, or
  seen by a human (screen) but not asserted by a program. Anything inferred
  carries the word.
- **Environment of the measurements**: Rex app "Rex Beta 0.1.0 (1541)"
  attached to the session, window showing the conductor's tab, one tiled block
  of 149x130 cells.

## Layer chrome

Single source for W3.T1 (`surface` sizing and `BoundsForCells`) and W5.T2
(constants injected into the generated Lua). Re-proved by
`TestLayerChromeDelta` and the final-size step of `TestPrewarmPlacement`,
which parse the constants table below from this section.
`TestChromeConstantsMatchBehaviorsDoc` ties both tables here to
`rexuikit/surface`, and `TestUsableMatchesRecordedSamples` replays every
sample in the evidence table through `surface.Usable`.

**The size depends on position.** The app lays a floating layer out in
pixels: it rounds each edge of the layer to a whole pixel on its own, takes
the border and title out in pixels, and cuts the rest into whole cells. Two
layers with the same `w` can differ by one column at different `x` (w=0.40
over 149 columns: 57 at x=0.40, 58 at x=0.55). Rows behave the same way.

Model, per axis. Inputs, all available to Go and to Lua when a layer is
opened: the window's cell size (`columns`, `rows` of the window's lone tiled
block, from `com.superlogical.terminal.size`), the cell size in pixels
(`cell_width_px`, `cell_height_px` from that same `size` result), and the
layer's fractional bounds `[x, y, w, h]`.

```
P_cols = window_cols * cell_width_px + FramePxCols            -- window px
P_rows = window_rows * cell_height_px + FramePxRows
edge_c(f) = round(P_cols * f)                                 -- EdgeRounding
edge_r(f) = round(P_rows * f)
usable_cols = floor((edge_c(x + w) - edge_c(x) - BorderPxCols) / cell_width_px)
usable_rows = floor((edge_r(y + h) - edge_r(y) - BorderPxRows) / cell_height_px)
```

`round` is half away from zero (`floor(v + 0.5)` for the non-negative values
here); `floor` is `SizeRounding`. Use float64 (Lua numbers are doubles).

| Constant       | Value |
|----------------|-------|
| `FramePxCols`  | 21    |
| `FramePxRows`  | 46    |
| `BorderPxCols` | 20    |
| `BorderPxRows` | 46    |
| `EdgeRounding` | round |
| `SizeRounding` | floor |

The frame offset is in pixels, not cells. Cols could be written either way
(1064 px = (149 + 3) * 7), but rows cannot: 130 rows of 14 px span 1866 px
in the app's layout, which is not a whole number of cells.

**Superseded model.** Until the review-fix re-measure this section recorded
`P = (cells + 3) * cell_px` on both axes with `BorderPxRows` 44 (1862 px for
rows), and said that border "never predicts more rows than 43 does, so an
inverse built on it errs toward one row too many, never one too few". That
was wrong. A centred 46x30 layer over 149x130 at 7x14 px (W6.T2, bounds
`0.339286, 0.375403, 0.321429, 0.249194`) spans exactly 30 * 14 + 44 px
under the old model, which said 30 rows; the app gave 29 every time. The old
42 samples fit 402 (window px, border px) pairs for rows, and 1862/44 was
only one of them. The 54 samples added since (row spans swept one pixel at a
time, then boxes picked to split the survivors) leave exactly one: 1866 px,
border 46, which gives that layer 29.

### Inverse for `BoundsForCells`

Given window cells `Wc x Hc`, cell px `cw x ch`, a desired usable size
`cols x rows` and an anchor (where the layer goes: a fixed `x`/`y`, centred,
or flush right/bottom), per axis (cols shown; rows use `Hc`, `ch`,
`FramePxRows`, `BorderPxRows`, `InverseMarginPxRows`):

1. `P = Wc * cw + FramePxCols`;
   `span_px = cols * cw + BorderPxCols + InverseMarginPxCols`.
2. `s = span_px / P` (the fraction that covers the span if both edges were
   exact).
3. Place: the anchor gives `x` from `s` (fixed `x`; centred `x = (1 - s) / 2`;
   right `x = 1 - s`). Clamp `x` to `[0, 1 - s]`.
4. Correct against the forward model, run with the margin added to the
   border: while that gives fewer than `cols` and `x + s < 1`, set
   `s += 1 / P` and re-place `x` from the anchor (step 3). Each step adds one
   pixel, so it ends within `cw` steps.
5. If `s` would exceed `1 - x` (and `x` cannot move left), the window is too
   small: return the largest layer (`s = 1 - x`) and report the shortfall.
   A window that holds `cols` only without the margin still reports success.

The result is the smallest fraction (to one pixel) that yields at least
`cols x rows` with the margin spare at that position. Always pass the same
`x`, `y` to `new_layer` that the inverse used: the size is only guaranteed
there.

| Constant              | Value |
|-----------------------|-------|
| `InverseMarginPxCols` | 0     |
| `InverseMarginPxRows` | 5     |

The margins are pixels the inverse keeps spare beyond the model. They are
not chrome, and the forward model (`surface.Usable`) never uses them. The
rows margin was added while rows were under-determined, to keep the W6.T2
layer from coming up a row short. The model above now reproduces that
layer, so the margin is no longer a correction: it stays because the model
was fit on one window and one cell size, and 5 px absorbs a frame or border
a few pixels off elsewhere. 5 px is under one row, so a form never gets a
row more than it asked for. Cols keep none: they fit one model from the
first 42 samples, including zero-spare samples the app honoured. The Lua
gets both values as `MARGIN_PX_COLS` and `MARGIN_PX_ROWS`.

### Evidence

96 samples, window 149x130 cells, cell 7x14 px, `rex do` script
`rexuikit/scripts/measure-chrome.lua` (re-run 2026-10-28 during review fix 1
with `fit=0`, layers opened over the conductor's window, each block
disowned, size read once the app client owned it and its size held for
300 ms, every layer closed; the fits below were run offline over the
printed samples). The first 42 rows are the 12:45 grid, re-measured with
identical results. Rows 43-62 are centred 46-column layers whose rows span
464 + k px (k = -3..16) under the old model, the first of them the W6.T2
layer. Rows 63-88 are the same spans flush top and at y = 0.5. Rows 89-96
were picked to separate the rows models still standing after that.

Searched, per axis, the pixel family above with `P` in
`W*cell - 40 .. W*cell + 80`, border 0..79 px, edge and size rounding each
in {floor, round, ceil}:

- **Cols:** only `P = 1064` with round edges and border 20 px under floor
  (round/ceil with border 23/26 px are the same function).
- **Rows:** only `P = 1866` with round edges and border 46 px under floor
  (round/ceil with border 53/59 px are the same function). After rows 1-88
  four functions were left (1866/46, 1870 with floor or ceil edges,
  1874/48); rows 89-96 rule out all but 1866/46.

The old position-free rule `floor((window + 3) * w) - 3` misses 9 of the
first 42 cols samples. The cell-edge family
`R((W + a) * (p + s) + B) - R((W + a) * p + B) - c` fits neither axis.

| bounds `x, y, w, h`             | cols | rows | model cols | model rows |
|---------------------------------|------|------|------------|------------|
| 0.400000, 0.300000, 0.400000, 0.300000 | 57 | 36 | 57 | 36 |
| 0.100000, 0.100000, 0.800000, 0.800000 | 118 | 103 | 118 | 103 |
| 0.250000, 0.200000, 0.500000, 0.600000 | 73 | 76 | 73 | 76 |
| 0.300000, 0.150000, 0.330000, 0.450000 | 47 | 56 | 47 | 56 |
| 0.050000, 0.050000, 0.370000, 0.710000 | 53 | 91 | 53 | 91 |
| 0.500000, 0.500000, 0.450000, 0.450000 | 65 | 56 | 65 | 56 |
| 0.000000, 0.000000, 0.200000, 0.200000 | 27 | 23 | 27 | 23 |
| 0.150000, 0.600000, 0.250000, 0.350000 | 35 | 43 | 35 | 43 |
| 0.350000, 0.050000, 0.600000, 0.250000 | 88 | 30 | 88 | 30 |
| 0.600000, 0.200000, 0.300000, 0.700000 | 42 | 90 | 42 | 90 |
| 0.200000, 0.400000, 0.650000, 0.150000 | 95 | 16 | 95 | 16 |
| 0.450000, 0.450000, 0.100000, 0.500000 | 12 | 63 | 12 | 63 |
| 0.020000, 0.300000, 0.550000, 0.550000 | 80 | 70 | 80 | 70 |
| 0.330000, 0.330000, 0.340000, 0.340000 | 48 | 42 | 48 | 42 |
| 0.000000, 0.000000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.050000, 0.650000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.100000, 0.100000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.150000, 0.550000, 0.400000, 0.300000 | 57 | 36 | 57 | 36 |
| 0.200000, 0.200000, 0.400000, 0.300000 | 57 | 36 | 57 | 36 |
| 0.250000, 0.450000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.300000, 0.400000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.350000, 0.350000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.450000, 0.250000, 0.400000, 0.300000 | 57 | 36 | 57 | 36 |
| 0.500000, 0.500000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.550000, 0.300000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.600000, 0.050000, 0.400000, 0.300000 | 58 | 36 | 58 | 36 |
| 0.550000, 0.300000, 0.200000, 0.200000 | 27 | 23 | 27 | 23 |
| 0.070000, 0.620000, 0.200000, 0.200000 | 27 | 23 | 27 | 23 |
| 0.410000, 0.110000, 0.330000, 0.450000 | 47 | 56 | 47 | 56 |
| 0.630000, 0.510000, 0.330000, 0.450000 | 47 | 56 | 47 | 56 |
| 0.130000, 0.170000, 0.500000, 0.600000 | 73 | 76 | 73 | 76 |
| 0.470000, 0.370000, 0.500000, 0.600000 | 73 | 76 | 73 | 76 |
| 0.310000, 0.090000, 0.650000, 0.150000 | 95 | 16 | 95 | 16 |
| 0.010000, 0.830000, 0.650000, 0.150000 | 95 | 16 | 95 | 16 |
| 0.493500, 0.493500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.496500, 0.496500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.499500, 0.499500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.502500, 0.502500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.505500, 0.505500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.508500, 0.508500, 0.300000, 0.300000 | 42 | 36 | 42 | 36 |
| 0.100000, 0.100000, 0.396600, 0.396600 | 57 | 49 | 57 | 49 |
| 0.100000, 0.100000, 0.403300, 0.403300 | 58 | 50 | 58 | 50 |
| 0.100000, 0.000000, 0.400000, 0.136900 | 58 | 14 | 58 | 14 |
| 0.100000, 0.021000, 0.400000, 0.106900 | 58 | 11 | 58 | 11 |
| 0.100000, 0.042000, 0.400000, 0.106900 | 58 | 11 | 58 | 11 |
| 0.100000, 0.063000, 0.400000, 0.196900 | 58 | 22 | 58 | 22 |
| 0.100000, 0.084000, 0.400000, 0.107200 | 58 | 11 | 58 | 11 |
| 0.100000, 0.112000, 0.400000, 0.286900 | 58 | 34 | 58 | 34 |
| 0.100000, 0.140000, 0.400000, 0.294100 | 58 | 35 | 58 | 35 |
| 0.100000, 0.175000, 0.400000, 0.279400 | 58 | 33 | 58 | 33 |
| 0.339286, 0.376208, 0.321429, 0.247583 | 46 | 29 | 46 | 29 |
| 0.339286, 0.375940, 0.321429, 0.248120 | 46 | 29 | 46 | 29 |
| 0.339286, 0.375671, 0.321429, 0.248657 | 46 | 29 | 46 | 29 |
| 0.339286, 0.375403, 0.321429, 0.249194 | 46 | 29 | 46 | 29 |
| 0.339286, 0.375134, 0.321429, 0.249731 | 46 | 30 | 46 | 30 |
| 0.339286, 0.374866, 0.321429, 0.250269 | 46 | 30 | 46 | 30 |
| 0.339286, 0.374597, 0.321429, 0.250806 | 46 | 30 | 46 | 30 |
| 0.339286, 0.374329, 0.321429, 0.251343 | 46 | 30 | 46 | 30 |
| 0.339286, 0.374060, 0.321429, 0.251880 | 46 | 30 | 46 | 30 |
| 0.339286, 0.373792, 0.321429, 0.252417 | 46 | 30 | 46 | 30 |
| 0.339286, 0.373523, 0.321429, 0.252954 | 46 | 30 | 46 | 30 |
| 0.339286, 0.373255, 0.321429, 0.253491 | 46 | 30 | 46 | 30 |
| 0.339286, 0.372986, 0.321429, 0.254028 | 46 | 30 | 46 | 30 |
| 0.339286, 0.372718, 0.321429, 0.254565 | 46 | 30 | 46 | 30 |
| 0.339286, 0.372449, 0.321429, 0.255102 | 46 | 30 | 46 | 30 |
| 0.339286, 0.372180, 0.321429, 0.255639 | 46 | 30 | 46 | 30 |
| 0.339286, 0.371912, 0.321429, 0.256176 | 46 | 30 | 46 | 30 |
| 0.339286, 0.371643, 0.321429, 0.256713 | 46 | 31 | 46 | 31 |
| 0.339286, 0.371375, 0.321429, 0.257250 | 46 | 31 | 46 | 31 |
| 0.339286, 0.371106, 0.321429, 0.257787 | 46 | 31 | 46 | 31 |
| 0.100000, 0.000000, 0.400000, 0.249194 | 58 | 29 | 58 | 29 |
| 0.100000, 0.500000, 0.400000, 0.249194 | 58 | 29 | 58 | 29 |
| 0.100000, 0.000000, 0.400000, 0.249731 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.249731 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.250269 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.250269 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.250806 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.250806 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.251343 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.251343 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.251880 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.251880 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.252417 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.252417 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.252954 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.252954 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.253491 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.253491 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.254028 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.254028 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.254565 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.254565 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.255102 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.255102 | 58 | 30 | 58 | 30 |
| 0.100000, 0.000000, 0.400000, 0.255639 | 58 | 30 | 58 | 30 |
| 0.100000, 0.500000, 0.400000, 0.255639 | 58 | 30 | 58 | 30 |

### Split windows

The model is measured on a window holding one tiled block. forge reads a
window's cell size from a tiled block (`surface.referenceBlock`, the Lua's
`window_cells` from the focused block) and scales it up by the block's
layout rect, which cannot see the space a split takes. Splitting the
user's tab to measure that would rearrange it, so `measure-chrome.lua`
splits a block inside a floating layer it opens instead (a 0.6 x 0.6 layer,
88x76 usable) and compares the scaled-up size of one half with the layer's
own size:

| split               | half | sizes after split | scaled up | short by |
|---------------------|------|-------------------|-----------|----------|
| horizontal 0.5      | a    | 42 + 42 cols      | 84 cols   | 4        |
| horizontal 0.7/0.3  | a    | 61 + 24 cols      | 87 cols   | 1        |
| horizontal 0.3/0.7  | a    | 24 + 61 cols      | 80 cols   | 8        |
| vertical 0.5        | a    | 36 + 36 rows      | 72 rows   | 4        |
| vertical 0.7/0.3    | a    | 52 + 20 rows      | 74 rows   | 2        |
| vertical 0.3/0.7    | a    | 20 + 52 rows      | 67 rows   | 9        |

(Two of the twelve reads in the two runs caught a half before the app had
resized it; the table uses the settled values.)

So a split block scaled up to its window comes out short, by about
`3 / f - 3` cells plus rounding for a block holding fraction `f` of the
axis: 4 cells at 50/50. `surface.referenceBlock` takes the largest block
(`f >= 0.5` in a two-way split), so the window it reports is up to about 4
cells short per split level; the Lua uses the focused block, which can be
the smaller one. Short is the safe side: a smaller window makes the inverse
ask for a larger fraction, so the layer comes out larger than the form,
never smaller, and only the `size` passed for the first frame is a little
small until the app resizes the block. Inferred: it assumes a split inside
a floating layer takes the same space as one in the tiled layer, which no
test has checked against a split tiled window.

Caveats (inferred):

- **One window size and one cell size.** All 96 samples come from 149x130
  cells at 7x14 px. The model, and in particular the pixel frame offsets,
  are inferred for other window and cell sizes; `TestLayerChromeDelta`
  re-measures on the live window and will catch a mismatch.

## Glyph

- **Rex draws a `>_` terminal glyph before every layer title, and no API
  field controls it.** Observed in the app on every layer; `session.new_layer`
  and `session.new_block` accept `label` only (`rex-api/api-help-r7.json`).
  The user dislikes it and accepts it. Rex feature-request candidate. No
  live test (visual; not readable through the API). `inferred`.

## Sizing and ownership

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| A layer opened without `size` starts at 80x24 and stays there until something sizes it. | `rex do` probe: layer `size` 80x24 for 10 s with no app claim; again in W3.T3 run. | observed | `TestLayerDefaultSize80x24` |
| The `size` option is honoured from the program's first `TIOCGWINSZ` read. | W3.T3 run: helper reports the requested size at startup. | observed | `TestLayerSizeOptionHonored` |
| The app only claims and resizes blocks it is displaying. A new window created from a script is not displayed until the user switches to it. | NO_APP_RESIZE: a focused new window's tiled block and layer stayed 80x24 for 10 s while the app was attached. Layers over the window the user was looking at resized within ~0.5 s. | observed | `TestLayerChromeDelta` (fails loudly, not skipped, when no app client claims blocks) |
| **Block ownership vs app sizing.** The connection that creates a block owns it (`terminal.size.owner` = creator). While a non-app client owns the block, the app's size request is not applied. `session.disown_block` right after creation hands it to the app. | Before: layers created by a `rex do` script stayed 80x24, `owner` = script client. After adding `disown_block` in `measure-chrome.lua`: all 14 layers got an app size and `owner` was null or the app client. One-shot `rex api call` creators resize correctly because they disconnect at once. | observed | `TestWarmBlockResizeBeforePlacement`, `TestLayerChromeDelta` |
| `terminal.resize` on an owned detached block applies at once and the program sees it. | W3.T3 run. | observed | `TestWarmBlockResizeBeforePlacement` |
| `terminal.size` returns `columns`, `rows`, `owner`, `desired`. After `disown_block`, `desired` stays empty unless the app is rendering the window. | `measure-chrome.lua` failure path prints them; W3.T3 run. | observed | `TestLayerChromeDelta` |
| `shell="none"` without `command` tries to exec the program named "none". | Planning probe. | inferred | none (a failure mode, not relied on) |

## Layer lifecycle and focus

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| A layer (and its block) disappears, with a `block_closed` event, when its program exits. | W3.T3 run. | observed | `TestLayerClosesOnProgramExit` |
| A detached block whose program exits also leaves `list_blocks` and emits `block_closed`. W5's live-process check can rely on it. | W3.T3 run. | observed | `TestDetachedBlockClosesOnProgramExit` |
| Default-background cells in a layer show the terminal beneath. | Seen by the user in the planning demo; not readable through the API. | inferred | none (visual); rexuikit paints every cell with an explicit background |
| Focus returns to the prior pane after a focused layer closes; the view lags briefly. | Seen by the user in planning. | inferred | none |
| A focus change emits only `session_view_changed{revision}`. | W3.T3 run: `focus_block` between blocks of one window emitted only that event for the test's blocks. | observed | `TestFocusChangeEmitsViewChanged` |
| Focus reports (DECSET 1004) are forwarded to the program. Planning saw each report twice; the W3.T3 run (no app rendering the window) saw exactly one `ESC[I` and one `ESC[O` per change. The doubling likely comes from the app forwarding its own focus as well, so dedupe must tolerate both. The helper must be in non-canonical mode: canonical mode holds `ESC[I` (no newline) in the line discipline. | Planning probe; W3.T3 run. | observed (count varies) | `TestFocusReportsForwarded` |
| `session.float_block` on a detached block fails with `not_found`. | W3.T3 run. | observed | `TestFloatBlockRejectsDetached` |
| Pre-warm placement: `new_layer(placeholder)` + `move_block(warm beside placeholder)` + `block.close(placeholder)` leaves the warm block as the sole block of a floating layer. `placement` flips `detached` to `placed`, `window_id` appears, `set_block_label` works, `focus_block` delivers `ESC[I`. ~10-15 ms in-server; the user saw it as instant. A detached warm block gets no resize within 4 s of placement unless the app renders the window. | W3.T3 run; planning probes. | observed | `TestPrewarmPlacement` |

## Listing, labels, ids

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| `session.list_blocks` lists detached blocks with `label` and `placement: "detached"`, and no `window_id` or `owner` once disowned. | Probe run, Rex r7: `{"block_id":"block:01a183e5-7bf6-...","label":"probe-detached","placement":"detached", ...}`. W3.T3 run. | observed | `TestListBlocksIncludesDetachedWithLabel` |
| Placed blocks carry `placement: "placed"`, `window_id`, `label`, `owner`. | Probe run: `placement":"placed","window_id":"window:01a1832e-4c50-..."`. | observed | `TestPrewarmPlacement` |
| `session.set_block_label` works on a placed block; the new label shows in `list_blocks` at once. | Probe run: `label` "shell" to "probe-relabel", result `{"revision":300}`. | observed | `TestPrewarmPlacement` |
| Rex starts every block's program with `REX_BLOCK` set to that block's own id (and `REX_SESSION`, `REX_SERVER`), also for a detached block made over the API. forge's warm program reads only `REX_BLOCK` from this, to take only a placement context that names its own block. | Probe run 2026-10-28 (review fix 1): `session.new_block` running `env`, detached, printed `REX_BLOCK=` the id `new_block` returned. | observed | `TestDetachedBlockEnvNamesOwnBlock` |
| Block ids are `block:<uuidv7>`, so lexical order is creation order. | Probe run: the detached block `...7bf6-76fd...` sorts before the layer block `...7bfa-7e8a...` created after it. Planning probe. | observed (one pair; uuidv7 is documented time-ordered) | none |
| From a `rex do` script, `rex.call("block.close", {block_id = ...})` returned without error but did not close the block; `rex api call block.close` with `session_id` did. Close detached blocks from a script through `rex api call` or an event, and always verify with `list_blocks`. | Probe run left `probe-detached` in `list_blocks` until closed by hand. | observed (once) | none |

## Terminal behavior inside a layer

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| **OSC 11 background-colour query gets a reply.** A layer program that sends `ESC ] 11 ; ? ESC \` in raw mode receives `ESC ] 11 ; rgb:0d0d/1111/1717 ESC \` (the terminal's background colour, ST-terminated) within 3 s. A bubbletea/termenv program can therefore detect background colour inside a layer. | Probe: python helper in a layer of the conductor window, reply written to a file and read back, run 2026-10-28, Rex r7. The reply colour is the Rex terminal background, not necessarily the user's theme at other times. | observed (once) | none in W3.T3's list; re-prove by re-running the probe (`rex do` layer running a raw-mode OSC 11 reader) |
| `terminal.format` returns the screen text in `content`; with `format="text"` and `trim=true` it supports polling for a marker. | `measure-ttfp.lua` found the marker in 15-17 polls. | observed | used by `measure-ttfp.lua` |

## `rex do` and Lua

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| **`rex do` runs with the calling CLI's environment**, not the server's: an exported shell variable is visible through `os.getenv`, and `PATH` is the caller's full PATH (homebrew, asdf, `~/bin`). So `rex do` cannot stand in for config-Lua's environment. Config-Lua `rex.exec` gets the Rex server's env, where PATH lacks `git` and `rex`. | Probe run: `FORGE_PROBE_VAR=exported-from-cli` read back inside `rex do`; PATH printed. Config-Lua side: planning probe and forge's own `rex.exec` wrapper. | `rex do` side observed; config-Lua side inferred | `TestLuaCanWriteFile` (doc comment) |
| `io.open` and `os.rename` work in `rex do` Lua. | Probe run: wrote `probe.tmp`, `os.rename` returned true, read back "hello". | observed | `TestLuaCanWriteFile` |
| `rex do` args arrive as `KEY=VALUE`; JSON-looking values are pre-decoded into Lua tables (`body={"a":1}` becomes a table; `tostring` writes `table: 0x...`). Pass plain tokens or re-encode with `rex.json.encode`. | W3.T3 run; `measure-ttfp.lua` accepts both forms. | observed | `TestLuaCanWriteFile` |
| `rex.now` does not exist; `os.clock` is CPU time, `os.time` whole seconds. A millisecond wall clock needs `rex.exec` of `/usr/bin/python3` (~27-32 ms per call here). | `measure-ttfp.lua` run: `exec_cost_ms=27` and `32`. | observed | none |

## Protocol

| Behavior | Evidence | Confidence | Test |
|---|---|---|---|
| The server negotiates `rex.control.v1`, api revision 7. | `server.version` -> `api_revision: 7`; `VERSION`. | observed | `TestNegotiatedRevision` |
| Ordered methods run one at a time per connection (the list is in the spec's "ordered" section: `block.close`, `session.new_layer`, `session.move_block`, ...). Results carry `ordered_after`, the previous ordered request id on the same connection; an 8-request burst serialised in order. | W3.T3 run. | observed | `TestOrderedMethodsSerialize` |
| Error codes are stable: `bad_request` (missing `session_id`), `method_not_found` (404, e.g. `terminal.size` is `com.superlogical.terminal.size`), `not_found`, `conflict`. | CLI error text in this run: `no handler for method "terminal.size" (status=404 code=method_not_found)`, `session_id is required (status=400 code=bad_request)`. | observed | `TestFloatBlockRejectsDetached` (`not_found`) |

## Latencies

All `observed`, this machine, Rex r7.

| Measure | Value | Source |
|---|---|---|
| `rex` CLI call | ~76 ms per call | planning probe |
| WebSocket call | 0.1-3 ms | planning probe |
| Floating layer open | ~2-3 ms | planning probe |
| Minimal bubbletea time-to-first-paint | ~85 ms warm | planning probe |
| First run of a freshly built binary | +~450 ms (macOS first-launch check) | planning probe |
| `forge quick new` time to first paint | ~390 ms warm (baseline) | planning probe via the `measure-ttfp.lua` method |
| `measure-ttfp.lua` against `/bin/sh -c 'echo MARK; sleep 3'` | 53-65 ms (poll 3 ms; own timestamp cost 27-32 ms subtracted) | run 2026-10-28; proves the script, not a forge number |

## Scripts

- `rexuikit/scripts/rex-api-snapshot.sh`: regenerates `rex-api/`; two runs are
  byte-identical (checked); exits 1 with a clear message when the socket is
  unreachable or `REX_SERVER` is malformed.
- `rexuikit/scripts/measure-chrome.lua`: `rex do -s "$REX_SESSION"
  rexuikit/scripts/measure-chrome.lua wid=<window> block=<tiled block>`.
  Opens a 42-layer grid over an existing window, never creates a window or
  split, closes every layer, and prints every cell-edge and pixel-edge fit;
  exits 2 with owner/desired/size when the app never claims a block.
- `rexuikit/scripts/measure-ttfp.lua`: `rex do -s "$REX_SESSION"
  rexuikit/scripts/measure-ttfp.lua cmd='<json argv>' cwd=<dir> marker=<text>
  wid=<window>`; prints `found=... ttfp_ms=... exec_cost_ms=... polls=...`.
