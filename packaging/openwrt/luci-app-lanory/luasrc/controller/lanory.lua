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
	--
	-- The node itself is declared in root/usr/share/luci/menu.d/luci-app-lanory.json
	-- so it exists even without the Lua runtime, and so its ACL group comes from
	-- root/usr/share/rpcd/acl.d/luci-app-lanory.json. This controller only
	-- registers the leaf pages; re-declaring the acl dependency here would make
	-- the node vanish whenever the Lua runtime is absent (LuCI 25.12 does not
	-- install it by default), because depends.acl is then only seen from Lua.
	entry({"admin", "services", "lanory"}, template("lanory/overview"), _("Lanory"), 60)

	-- Child routes stay addressable but untitled: React owns the only tab bar.
	entry({"admin", "services", "lanory", "overview"}, template("lanory/overview"))
	-- Preserve old bookmarks without maintaining a second device page.
	entry({"admin", "services", "lanory", "devices"}, call("action_devices"))
	entry({"admin", "services", "lanory", "session"}, post("action_session")).leaf = true
	entry({"admin", "services", "lanory", "config"}, cbi("lanory"))
end

-- The dispatcher enforces an authenticated LuCI session and its CSRF token
-- before this POST handler. The credential is never placed in HTML or a URL.
function action_session()
 local http = require "luci.http"
 http.header("Cache-Control", "no-store")
 http.header("Pragma", "no-cache")
 http.prepare_content("application/json")
 local access = require("luci.util").ubus("session", "access", {
  ubus_rpc_session = luci.dispatcher.context.authsession,
  scope = "access-group", object = "luci-app-lanory", ["function"] = "write"
 })
 if not access or not access.access then
  http.status(403, "Forbidden")
  http.write_json({ error = "Lanory write permission required" })
  return
 end
 local fd = io.open("/etc/lanory/token", "r")
 local token = fd and fd:read("*l") or ""
 if fd then fd:close() end
 if token == "" then
  http.status(503, "Service Unavailable")
  http.write_json({ error = "Lanory credential unavailable" })
  return
 end
 http.write_json({ token = token })
end

function action_devices()
 luci.http.redirect(luci.dispatcher.build_url("admin", "services", "lanory", "overview") .. "#/devices")
end
