-- luawrite.lua: TestLuaCanWriteFile's `rex do` script.
--
--   rex do luawrite.lua dir=<directory> name=<file name> body=<text>
--
-- Writes body to dir/name.tmp with io.open, then renames it to dir/name with
-- os.rename: the same temp-file + rename the generated forge Lua uses for the
-- pre-warm context file. Prints "ok" on success and stops with code 1 on any
-- failure, naming the call that failed.

local args = rex.args
local dir, name, body = args.dir, args.name, args.body
if not dir or not name or not body then
  rex.log("error", "usage: rex do luawrite.lua dir=<dir> name=<name> body=<text>")
  rex.stop(2)
  return
end

local final = dir .. "/" .. name
local tmp = final .. ".tmp"
local f, err = io.open(tmp, "w")
if not f then
  rex.log("error", "io.open: " .. tostring(err))
  rex.stop(1)
  return
end
f:write(tostring(body))
f:close()
local ok, rerr = os.rename(tmp, final)
if not ok then
  rex.log("error", "os.rename: " .. tostring(rerr))
  rex.stop(1)
  return
end
print("ok")
