-- Licensed to the public under the MIT License.

module("luci.controller.lanory", package.seeall)

function index()
	if not nixio.fs.access("/etc/config/lanory") then
		return
	end

	local page = entry({"admin", "network", "lanory"}, firstchild(), _("Lanory"), 60)
	page.dependent = false
	page.acl_depends = { "luci-app-lanory" }

	entry({"admin", "network", "lanory", "overview"}, template("lanory/overview"), _("Overview"), 10)
	entry({"admin", "network", "lanory", "config"}, cbi("lanory"), _("Settings"), 20)
	entry({"admin", "network", "lanory", "status"}, call("action_status")).leaf = true
end

-- action_status returns JSON for the overview page, so the LuCI UI never has to
-- scrape the Lanory API itself (which may be bound to a different port).
function action_status()
	local uci = require "luci.model.uci".cursor()
	local port = uci:get("lanory", "main", "port") or "8787"
	local out = { running = false, url = "http://" .. luci.http.getenv("SERVER_NAME") .. ":" .. port .. "/" }

	local pid = luci.sys.exec("pidof lanory 2>/dev/null")
	if pid and pid:match("%d") then
		out.running = true
	end
	luci.http.prepare_content("application/json")
	luci.http.write_json(out)
end
