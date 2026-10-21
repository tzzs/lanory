-- Syntax-check a LuCI template by translating <% %> blocks into Lua.
-- LuCI shorthand:
--   <%# comment %>
--   <%= expr %>       -> translate(expr)
--   <%: text %>       -> translate("text")
--   <% code %>
local fs = require "nixio.fs"
local path = arg[1] or "/usr/lib/lua/luci/view/lanory/overview.htm"
local src = fs.readfile(path)
if not src then print("cannot read " .. path); return end

local lua_src = "local _ = function(x) return x end\nlocal translate = _\n"
local blocks, exprs, texts = 0, 0, 0
for code in src:gmatch("<%%(.-)%%>") do
  if code:match("^#") then
    -- comment
  elseif code:match("^:") then
    texts = texts + 1
    lua_src = lua_src .. "local _t = translate(" .. string.format("%q", code:sub(2)) .. ")\n"
  elseif code:match("^=") then
    exprs = exprs + 1
    lua_src = lua_src .. "local _x = translate(" .. code:sub(2) .. ")\n"
  else
    blocks = blocks + 1
    lua_src = lua_src .. code .. "\n"
  end
end
local f, err = loadstring(lua_src)
print(string.format("%s: code=%d expr=%d text=%d compiles=%s %s",
  path:match("[^/]+$"), blocks, exprs, texts, tostring(f ~= nil), err or ""))
