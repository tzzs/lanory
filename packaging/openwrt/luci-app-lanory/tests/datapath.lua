-- Exercise exactly the data path the template uses.
local sys = { exec = function() return "1234" end }
local port = "8787"
local running = true

local token = ""
local fd = io.open("/etc/lanory/token", "r")
if fd then token = fd:read("*l") or ""; fd:close() end

local function api(path)
  local pipe = io.popen(string.format(
    "curl -s -m 5 -H 'Authorization: Bearer %s' 'http://127.0.0.1:%s%s'", token, port, path))
  if not pipe then return nil end
  local body = pipe:read("*a"); pipe:close()
  if not body or body == "" then return nil end
  local jsonc = require("luci.jsonc")
  local ok, decoded = pcall(function() return jsonc.parse(body) end)
  if ok and type(decoded) == "table" then return decoded end
  return nil
end

local overview = api("/api/v1/overview")
local monitoring = api("/api/v1/monitoring/status")

print("overview:", overview ~= nil)
print("monitoring:", monitoring ~= nil)
if overview and overview.counts then
  print(string.format("  total=%d online=%d offline=%d sleeping=%d",
    overview.counts.total, overview.counts.online, overview.counts.offline, overview.counts.sleeping))
end
if monitoring and monitoring.interfaces then
  print("  interfaces:")
  for _, i in ipairs(monitoring.interfaces) do
    local it = i.interface or {}
    print(string.format("    %-8s %-8s %5s Mbps %s",
      it.name, it.role or "-", tostring(it.speed_mbps), it.carrier and "up" or "down"))
  end
end
