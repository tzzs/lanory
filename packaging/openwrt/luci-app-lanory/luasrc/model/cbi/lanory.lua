-- Licensed to the public under the MIT License.

local fs = require "nixio.fs"
local sys  = require "luci.sys"
local http = require "luci.http"
local uci = require "luci.model.uci".cursor()

local m, s, o
local port = uci:get("lanory", "main", "port") or "8787"

m = Map("lanory", translate("Lanory"),
	translate("Self-hosted home network topology and monitoring. " ..
		"Lanory only reads the router's state; it never changes network configuration."))

s = m:section(NamedSection, "main", "lanory", translate("Service"))
s.anonymous = true
s.addremove = false

o = s:option(Flag, "enabled", translate("Enable"),
	translate("Start Lanory on boot."))
o.default = "1"
o.rmempty = false

o = s:option(Value, "listen", translate("Listen address"),
	translate("Keep 0.0.0.0 for LAN access, or use 127.0.0.1 behind a reverse proxy."))
o.datatype = "ipaddr"
o.default = "0.0.0.0"

o = s:option(Value, "port", translate("Port"))
o.datatype = "port"
o.default = "8787"

o = s:option(Value, "base_path", translate("Base path"),
	translate("Optional URL prefix when mounted behind a reverse proxy, for example /lanory."))
o.placeholder = "/lanory"

o = s:option(Value, "data_dir", translate("Data directory"),
	translate("Where the database lives. Preserved across upgrades and uninstalls."))
o.datatype = "directory"
o.default = "/var/lib/lanory"

return m
