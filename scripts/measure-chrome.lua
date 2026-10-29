-- measure-chrome.lua: measure the cell cost of Rex's floating-layer chrome.
--
-- Run unsandboxed (the Rex socket), against a window the Rex app is showing:
--
--   rex do -s "$REX_SESSION" rexuikit/scripts/measure-chrome.lua \
--     wid=window:<uuid> block=block:<uuid> [fit=0] [split=0]
--
-- fit=0 skips the model searches and only prints samples (fit them offline;
-- the pixel search over ~90 samples is slow in Rex's Lua). split=0 skips the
-- split probe at the end.
--
-- wid is the window to open layers over (the one the user is looking at);
-- block is the lone tiled block of that window, read only for its `size`
-- (the window's cell size) and its owner (the app client). The script
-- creates no window and no split, and never sends input to any block. Every
-- layer it opens is closed again.
--
-- Two Rex behaviours shape it:
--  * The app only resizes blocks it is displaying, so the window must be
--    visible. If a block never gets an app owner the script exits 2 with
--    NO_APP_RESIZE and prints the block's owner/desired/size.
--  * The creator of a block owns it, and the app's size request is not
--    applied while a non-app client owns it. A `rex do` script is such a
--    client, so each block is disowned (`session.disown_block`) right after
--    creation. See rex-behaviors.md "Block ownership vs app sizing".
--
-- Method: open a layer at fractional bounds [x,y,w,h] with NO `size`, wait
-- until the app client (the tiled block's owner) owns it and its size has held
-- for 300ms, read `size`, close the layer. The grid varies x and y as well as
-- w and h, including x/y steps that straddle cell boundaries, because the
-- size depends on position: the app rounds each layer edge on its own.
--
-- Models searched, per axis (W = window cells on that axis). Cell edges:
--   edge(f) = rounding(A * f + B)          A = W + a
--   usable  = edge(pos + frac) - edge(pos) - c
-- over rounding in {floor, round, ceil}, a in -4..8 step 0.25, B in -1..1
-- step 0.25, c in -2..8. Every (rounding, a, B, c) that reproduces EVERY
-- sample is printed. When none does, the best one is printed with its misses.
--
-- Output lines are machine-readable key=value, one per line:
--   window cols=W rows=H cell_px=WxH app=<client id>
--   sample bounds=x,y,w,h cols=C rows=R
--   rule axis=cols|rows rounding=<mode> a=<a> b=<B> chrome=<c> samples=<n>
--   best axis=... misses=<k> ... / miss axis=... bounds=... got=G want=P
--   split dir=<d> ratio=<r> layer=CxR a=CxR b=CxR rect_a=x,y,w,h est=CxR
--   done samples=<n> window=WxH

local TERM = "com.superlogical.terminal"
local SHELL = TERM .. ".shell"
local PARK = { "/bin/sleep", "600" }

local function block(cmd)
  return rex.layout.block { flavor = SHELL, options = { shell = "none", command = cmd } }
end

local function size_of(bid)
  return rex.block.call(TERM, "size", { block_id = bid })
end

local args = rex.args
local wid, tiled = args.wid, args.block
if not wid or not tiled then
  rex.log("error", "usage: rex do measure-chrome.lua wid=<window id> block=<tiled block id>")
  rex.stop(2)
  return
end

-- Window cell size: a lone tiled block fills the window. Its owner is the
-- app client that renders the window.
local ws = size_of(tiled)
local W, H, app = ws.columns, ws.rows, ws.owner
if not app or app == "" then
  rex.log("error", "NO_APP_RESIZE: the tiled block has no owner; the Rex app is not rendering this window")
  rex.stop(2)
  return
end
print(string.format("window cols=%d rows=%d cell_px=%sx%s app=%s", W, H,
  tostring(ws.cell_width_px), tostring(ws.cell_height_px), app))

-- wait_app polls until the app client owns the block and its size has held
-- for 300ms (the app may resize twice while its layout settles).
local function wait_app(bid, timeout)
  local waited, stable, lc, lr = 0, nil, -1, -1
  while waited < timeout do
    local s = size_of(bid)
    if s.owner ~= app then
      stable = nil
    elseif stable == nil or s.columns ~= lc or s.rows ~= lr then
      stable = waited
    elseif waited - stable >= 0.3 then
      return s.columns, s.rows
    end
    lc, lr = s.columns, s.rows
    rex.sleep(0.03)
    waited = waited + 0.03
  end
  return nil
end

-- Six places: the row samples below are built to land layer edges on whole
-- pixels, and four places would round them off when the output is re-fit.
local function fmt(n) return string.format("%.6f", n) end

-- The grid. Each row is {x, y, w, h}; x+w <= 1 and y+h <= 1.
local boxes = {
  -- W1.T2's original 14 (one x per width).
  { 0.40, 0.30, 0.40, 0.30 }, { 0.10, 0.10, 0.80, 0.80 }, { 0.25, 0.20, 0.50, 0.60 },
  { 0.30, 0.15, 0.33, 0.45 }, { 0.05, 0.05, 0.37, 0.71 }, { 0.50, 0.50, 0.45, 0.45 },
  { 0.00, 0.00, 0.20, 0.20 }, { 0.15, 0.60, 0.25, 0.35 }, { 0.35, 0.05, 0.60, 0.25 },
  { 0.60, 0.20, 0.30, 0.70 }, { 0.20, 0.40, 0.65, 0.15 }, { 0.45, 0.45, 0.10, 0.50 },
  { 0.02, 0.30, 0.55, 0.55 }, { 0.33, 0.33, 0.34, 0.34 },
  -- Position sweep: one size, x and y walked across the window.
  { 0.00, 0.00, 0.40, 0.30 }, { 0.05, 0.65, 0.40, 0.30 }, { 0.10, 0.10, 0.40, 0.30 },
  { 0.15, 0.55, 0.40, 0.30 }, { 0.20, 0.20, 0.40, 0.30 }, { 0.25, 0.45, 0.40, 0.30 },
  { 0.30, 0.40, 0.40, 0.30 }, { 0.35, 0.35, 0.40, 0.30 }, { 0.45, 0.25, 0.40, 0.30 },
  { 0.50, 0.50, 0.40, 0.30 }, { 0.55, 0.30, 0.40, 0.30 }, { 0.60, 0.05, 0.40, 0.30 },
  -- Same sizes as earlier rows, other positions.
  { 0.55, 0.30, 0.20, 0.20 }, { 0.07, 0.62, 0.20, 0.20 },
  { 0.41, 0.11, 0.33, 0.45 }, { 0.63, 0.51, 0.33, 0.45 },
  { 0.13, 0.17, 0.50, 0.60 }, { 0.47, 0.37, 0.50, 0.60 },
  { 0.31, 0.09, 0.65, 0.15 }, { 0.01, 0.83, 0.65, 0.15 },
  -- Fine steps straddling cell boundaries near the middle, both axes.
  { 0.4935, 0.4935, 0.30, 0.30 }, { 0.4965, 0.4965, 0.30, 0.30 },
  { 0.4995, 0.4995, 0.30, 0.30 }, { 0.5025, 0.5025, 0.30, 0.30 },
  { 0.5055, 0.5055, 0.30, 0.30 }, { 0.5085, 0.5085, 0.30, 0.30 },
  -- Fine width/height steps at one position.
  { 0.10, 0.10, 0.3966, 0.3966 }, { 0.10, 0.10, 0.4033, 0.4033 },
  -- Row discriminators (2026-10-28 review fix): after the pixel-span sweep
  -- below, rows still fit window px 1866 (border 46), 1870 (floor or ceil
  -- edges) and 1874 (border 48) over 149x130 at 7x14 px. Each of these
  -- boxes is predicted differently by at least two of those.
  { 0.10, 0.000, 0.40, 0.1369 }, { 0.10, 0.021, 0.40, 0.1069 },
  { 0.10, 0.042, 0.40, 0.1069 }, { 0.10, 0.063, 0.40, 0.1969 },
  { 0.10, 0.084, 0.40, 0.1072 }, { 0.10, 0.112, 0.40, 0.2869 },
  { 0.10, 0.140, 0.40, 0.2941 }, { 0.10, 0.175, 0.40, 0.2794 },
}

-- Row spans built in pixels. The 42 boxes above leave the rows model
-- under-determined (window px 1858..1878 all fit). A centred 46x30 form
-- (W6.T2) spans 30 rows + 44 px of chrome = 464 px over P = (H + 3) * ch:
-- the model said 30 rows, the app gave 29. These boxes sweep spans of
-- 464 + k px, centred, flush top and at y = 0.5, so every candidate
-- (window px, border px) pair predicts some of them differently. They are
-- computed from the live window, so they assume the same 3-row offset; on a
-- 149x130 window at 7x14 px the first centred one is exactly W6.T2's layer.
do
  local ch, cw = ws.cell_height_px, ws.cell_width_px
  if ch and ch > 0 and cw and cw > 0 then
    local P = (H + 3) * ch
    local Pc = (W + 3) * cw
    local sw = (46 * cw + 20) / Pc
    local cx = (1 - sw) / 2
    for k = -3, 16 do
      local h = (30 * ch + 44 + k) / P
      boxes[#boxes + 1] = { cx, (1 - h) / 2, sw, h }
    end
    for k = 0, 12 do
      local h = (30 * ch + 44 + k) / P
      boxes[#boxes + 1] = { 0.10, 0.0, 0.40, h }
      boxes[#boxes + 1] = { 0.10, 0.5, 0.40, h }
    end
  end
end

local samples = {}
for _, b in ipairs(boxes) do
  local layer = rex.session.new_layer {
    window_id = wid, bounds = b, focus = false, layout = block(PARK),
  }
  local bid = layer.block_ids[1]
  rex.session.disown_block { block_ids = { bid } }
  local c, r = wait_app(bid, 6)
  if not c then
    local s = size_of(bid)
    rex.log("error", string.format("NO_APP_RESIZE: bounds=%s,%s,%s,%s size=%dx%d owner=%s desired=%s",
      fmt(b[1]), fmt(b[2]), fmt(b[3]), fmt(b[4]), s.columns, s.rows, tostring(s.owner), rex.json.encode(s.desired)))
    rex.session.close_layer { layer_id = layer.layer_id }
    rex.stop(2)
    return
  end
  rex.session.close_layer { layer_id = layer.layer_id }
  print(string.format("sample bounds=%s,%s,%s,%s cols=%d rows=%d", fmt(b[1]), fmt(b[2]), fmt(b[3]), fmt(b[4]), c, r))
  samples[#samples + 1] = { b = b, cols = c, rows = r }
end

local modes = {
  floor = math.floor,
  round = function(x) return math.floor(x + 0.5) end,
  ceil = math.ceil,
}
local order = { "floor", "round", "ceil" }

local function predict(f, A, B, c, pos, frac)
  return f(A * (pos + frac) + B) - f(A * pos + B) - c
end

local function derive(axis)
  local total = (axis == "cols") and W or H
  local pi, fi, gk = 1, 3, "cols"
  if axis == "rows" then pi, fi, gk = 2, 4, "rows" end
  local fits, best = {}, nil
  for _, name in ipairs(order) do
    local f = modes[name]
    for ai = -16, 32 do
      local a = ai / 4
      for bi = -4, 4 do
        local B = bi / 4
        for c = -2, 8 do
          local misses = 0
          for _, s in ipairs(samples) do
            if predict(f, total + a, B, c, s.b[pi], s.b[fi]) ~= s[gk] then misses = misses + 1 end
          end
          if misses == 0 then fits[#fits + 1] = { mode = name, a = a, B = B, c = c } end
          if not best or misses < best.misses then
            best = { mode = name, a = a, B = B, c = c, misses = misses }
          end
        end
      end
    end
  end
  return fits, best, total, pi, fi, gk
end

local function show(axis)
  local fits, best, total, pi, fi, gk = derive(axis)
  for _, f in ipairs(fits) do
    print(string.format("rule axis=%s rounding=%s a=%g b=%g chrome=%d samples=%d", axis, f.mode, f.a, f.B, f.c, #samples))
  end
  if #fits == 0 then
    print(string.format("rule axis=%s fit=none", axis))
    print(string.format("best axis=%s rounding=%s a=%g b=%g chrome=%d misses=%d samples=%d",
      axis, best.mode, best.a, best.B, best.c, best.misses, #samples))
    local f = modes[best.mode]
    for _, s in ipairs(samples) do
      local want = predict(f, total + best.a, best.B, best.c, s.b[pi], s.b[fi])
      if want ~= s[gk] then
        print(string.format("miss axis=%s bounds=%s,%s,%s,%s got=%d want=%d", axis,
          fmt(s.b[1]), fmt(s.b[2]), fmt(s.b[3]), fmt(s.b[4]), s[gk], want))
      end
    end
  end
end
if args.fit ~= 0 and args.fit ~= "0" then
show("cols")
show("rows")

-- Pixel family (hypothesis: the app lays layers out in px, not cells). The
-- window is P px on the axis, each edge is rounded to a whole px, and the
-- chrome costs b px before the remainder is cut into cells:
--   e(f)   = r1(P * f)
--   usable = r2((e(pos + frac) - e(pos) - b) / cell_px)
-- Searched over P in (W*cell - 40)..(W*cell + 80), b in 0..79, r1 and r2 in
-- {floor, round, ceil}. The 21 samples on record (W1.T2's 14, W3.T3 run 3's
-- 7) fit cols only with P = 1064 = (149 + 3) * 7, r1 = round, b = 20,
-- r2 = floor; rows stay ambiguous without y varied.
local cellpx = { cols = ws.cell_width_px, rows = ws.cell_height_px }
local function pixel(axis)
  local total = (axis == "cols") and W or H
  local pi, fi = 1, 3
  if axis == "rows" then pi, fi = 2, 4 end
  local cell = cellpx[axis]
  if not cell or cell <= 0 then
    print(string.format("pixel axis=%s fit=skipped cell_px=%s", axis, tostring(cell)))
    return
  end
  local n = 0
  for P = total * cell - 40, total * cell + 80 do
    for _, n1 in ipairs(order) do
      local f1 = modes[n1]
      for b = 0, 79 do
        for _, n2 in ipairs(order) do
          local f2 = modes[n2]
          local ok = true
          for _, s in ipairs(samples) do
            local p, fr = s.b[pi], s.b[fi]
            if f2((f1(P * (p + fr)) - f1(P * p) - b) / cell) ~= s[axis] then ok = false break end
          end
          if ok then
            n = n + 1
            print(string.format("pixel axis=%s P=%d window_plus=%g edge=%s border_px=%d size=%s cell_px=%d samples=%d",
              axis, P, P / cell - total, n1, b, n2, cell, #samples))
          end
        end
      end
    end
  end
  if n == 0 then print(string.format("pixel axis=%s fit=none", axis)) end
end
pixel("cols")
pixel("rows")
end

-- Split probe. forge reads a window's cell size from a tiled block and
-- scales it up by the block's layout rect, which cannot see split borders.
-- Splitting the user's window to measure that would rearrange their tab, so
-- the split is made inside a floating layer this script opens: the layer's
-- unsplit size is known, and est is what that scaling gives from one half.
-- The error est - layer is the error a split tiled window would carry,
-- assuming a split inside a layer draws the same border as one in the tiled
-- layer.
local function rect_of(layer_id, bid)
  local v = rex.session.view {}
  for _, w in ipairs(v.windows or {}) do
    for _, l in ipairs(w.layers or {}) do
      if l.layer_id == layer_id then
        local function walk(n)
          if n == nil then return nil end
          if n.block_id == bid then return n.rect end
          if n.split then return walk(n.split.before) or walk(n.split.after) end
          return walk(n.before) or walk(n.after)
        end
        return walk(l.layout)
      end
    end
  end
  return nil
end

if args.split ~= 0 and args.split ~= "0" then
  local bounds = { 0.10, 0.10, 0.60, 0.60 }
  for _, dir in ipairs({ "horizontal", "vertical" }) do
    for _, ratio in ipairs({ 0.5, 0.3, 0.7 }) do
      local layer = rex.session.new_layer {
        window_id = wid, bounds = bounds, focus = false, layout = block(PARK),
      }
      local a = layer.block_ids[1]
      rex.session.disown_block { block_ids = { a } }
      local lc, lr = wait_app(a, 6)
      local res, err = rex.session.new_split {
        anchor_id = a, direction = dir, side = "after", ratio = ratio, focus = false,
        layout = block(PARK),
      }
      if res == nil or lc == nil then
        print(string.format("split dir=%s ratio=%g error=%s", dir, ratio, tostring(err or "NO_APP_RESIZE")))
        rex.session.close_layer { layer_id = layer.layer_id }
      else
        local b
        for _, id in ipairs(res.block_ids or {}) do if id ~= a then b = id end end
        if b == nil then
          local v = rex.session.list_blocks {}
          for _, x in ipairs(v.blocks or {}) do
            if x.block_id ~= a and x.layer_id == layer.layer_id then b = x.block_id end
          end
        end
        if b then rex.session.disown_block { block_ids = { b } } end
        rex.sleep(0.3)
        local ac, ar = wait_app(a, 6)
        local bc, br = -1, -1
        if b then bc, br = wait_app(b, 6) end
        local r = rect_of(layer.layer_id, a)
        local ec, er = -1, -1
        if r and ac then
          ec = math.floor(ac / r[3] + 0.5)
          er = math.floor(ar / r[4] + 0.5)
        end
        print(string.format("split dir=%s ratio=%g layer=%dx%d a=%sx%s b=%sx%s rect_a=%s est=%dx%d",
          dir, ratio, lc, lr, tostring(ac), tostring(ar), tostring(bc), tostring(br),
          r and string.format("%s,%s,%s,%s", fmt(r[1]), fmt(r[2]), fmt(r[3]), fmt(r[4])) or "nil", ec, er))
        rex.session.close_layer { layer_id = layer.layer_id }
      end
    end
  end
end

print(string.format("done samples=%d window=%dx%d", #samples, W, H))
