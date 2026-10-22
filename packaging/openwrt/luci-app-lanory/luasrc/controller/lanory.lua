-- Licensed to the public under the MIT License.

module("luci.controller.lanory", package.seeall)

-- Lanory's own port, read from the service configuration.
local function lanory_port()
	local ok, uci = pcall(require, "luci.model.uci")
	if ok and uci then
		return uci.cursor():get("lanory", "main", "port") or "8787"
	end
	return "8787"
end

-- lanory_api performs a GET against the Lanory REST API.
--
-- The token is read from the router itself rather than from the session: the
-- operator has already authenticated to LuCI, and asking for a second secret
-- inside the router's own UI would be theatre, not security.
local function lanory_api(path)
	local port = lanory_port()
	local token = ""

	local fd = io.open("/etc/lanory/token", "r")
	if fd then
		token = fd:read("*l") or ""
		fd:close()
	end

	local cmd = string.format(
		"curl -s -m 5 -H 'Authorization: Bearer %s' 'http://127.0.0.1:%s%s'",
		token, port, path)

	local pipe = io.popen(cmd)
	if not pipe then
		return nil
	end
	local out = pipe:read("*a")
	pipe:close()
	return out
end

-- lanory_json decodes a Lanory API response, returning nil when the call
-- failed or the body is not JSON.
local function lanory_json(path)
	local body = lanory_api(path)
	if not body or body == "" then
		return nil
	end
	local ok, jsonc = pcall(require, "luci.jsonc")
	if not ok or not jsonc then
		return nil
	end
	local pok, decoded = pcall(function() return jsonc.parse(body) end)
	if pok and type(decoded) == "table" then
		return decoded
	end
	return nil
end

function index()
	if not nixio.fs.access("/etc/config/lanory") then
		return
	end

	-- The menu lives under Services, not under Network.
	--
	-- Two reasons, both observed on iStoreOS 25.12.5:
	--
	--  1. iStoreOS's Docker plugin registers "admin/network" as an ALIAS to
	--     admin/services/dockerman/networks. Anything mounted under
	--     admin/network by a third-party package therefore never appears in the
	--     menu, even though it is registered and satisfied in the index.
	--  2. Every other daemon-style application on this firmware - appfilter,
	--     ddnsto, linkease, openclash, oled - lives under Services. Lanory is a
	--     background service with its own UI, so that is where a user looks.
	local page = entry({"admin", "services", "lanory"}, firstchild(), _("Lanory"), 60)
	page.dependent = false
	page.acl_depends = { "luci-app-lanory" }

	entry({"admin", "services", "lanory", "overview"}, template("lanory/overview"), _("Overview"), 10)
	entry({"admin", "services", "lanory", "devices"}, template("lanory/devices"), _("Devices"), 20)
	entry({"admin", "services", "lanory", "config"}, cbi("lanory"), _("Settings"), 30)
end

-- action_scan triggers a discovery run.
--
-- This is a write action, so it is a POST and the page reloads afterwards.
function action_scan()
	luci.http.prepare_content("application/json")
	local port = lanory_port()
	local token = ""
	local fd = io.open("/etc/lanory/token", "r")
	if fd then
		token = fd:read("*l") or ""
		fd:close()
	end

	local pipe = io.popen(string.format(
		"curl -s -m 10 -X POST -H 'Authorization: Bearer %s' " ..
		"-H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' " ..
		"-d '{}' 'http://127.0.0.1:%s/api/v1/discovery/scan'", token, port))
	local out = ""
	if pipe then
		out = pipe:read("*a") or ""
		pipe:close()
	end
	luci.http.write(out)
end
