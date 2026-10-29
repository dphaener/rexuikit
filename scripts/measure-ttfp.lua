-- measure-ttfp.lua: time from layer open to a marker being visible.
--
-- This is the method that produced the ~390ms `forge quick new` baseline,
-- written down so W5.T1 and W6.T2 measure the same way.
--
--   rex do rexuikit/scripts/measure-ttfp.lua \
--     cmd='["/tmp/forge-check","quick","new","--cwd","/path"]' \
--     cwd=/path marker='Session name' wid=window:<uuid>
--
-- Args (rex.args): cmd (JSON argv; `rex do` pre-decodes JSON values, a string
-- is decoded here for safety), cwd, marker (a substring that appears only once
-- the first frame is painted), wid (window to open the layer in).
--
-- Run unsandboxed (the Rex socket) with the window shown by the Rex app.
-- The layer opens without `size`, so a program that sizes itself from the
-- terminal sees 80x24 until the app resizes it; pass explicit bounds through
-- `bounds` (JSON [x,y,w,h]) to match what the program will really get.
--
-- The poll reads `terminal.format` every 3ms. Each timestamp is an external
-- process (Lua has no wall clock in ms), so its measured cost is subtracted:
-- ttfp_ms = (t1 - t0) - cost_of_one_exec_pair. Output: one `ttfp_ms=<n>` line.

local args = rex.args
local wid, marker = args.wid, args.marker
local cmd = args.cmd
if type(cmd) == "string" then cmd = rex.json.decode(cmd) end
local bounds = args.bounds
if type(bounds) == "string" then bounds = rex.json.decode(bounds) end
bounds = bounds or { 0.1, 0.1, 0.8, 0.8 }

if not wid or not marker or not cmd then
  rex.log("error", "usage: rex do measure-ttfp.lua cmd=<json argv> cwd=<dir> marker=<text> wid=<window id>")
  rex.stop(2)
  return
end

local TERM = "com.superlogical.terminal"
local function now()
  return tonumber(rex.exec({ "/usr/bin/python3", "-c", "import time;print(int(time.time()*1000))" }).stdout)
end

-- Cost of one timestamp call: two back-to-back reads bracket exactly one
-- exec, which is what t0 and t1 each add to the interval.
local a = now()
local b = now()
local exec_cost = b - a

local t0 = now()
local layer, err = rex.session.new_layer {
  window_id = wid, bounds = bounds, focus = false,
  layout = rex.layout.block {
    flavor = TERM .. ".shell",
    options = { shell = "none", command = cmd, cwd = args.cwd },
  },
}
if not layer then
  rex.log("error", "new_layer: " .. tostring(err))
  rex.stop(1)
  return
end
local bid = layer.block_ids[1]

local found, polls = false, 0
while polls < 1000 do
  polls = polls + 1
  local f = rex.block.call(TERM, "format", { block_id = bid, format = "text", trim = true })
  if f and f.content and string.find(f.content, marker, 1, true) then
    found = true
    break
  end
  rex.sleep(0.003)
end
local t1 = now()

print(string.format("found=%s ttfp_ms=%d exec_cost_ms=%d polls=%d", tostring(found), t1 - t0 - exec_cost, exec_cost, polls))
rex.sleep(0.5)
rex.session.close_layer { layer_id = layer.layer_id }
if not found then rex.stop(1) end
