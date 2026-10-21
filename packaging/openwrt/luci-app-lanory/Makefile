# luci-app-lanory — optional LuCI integration.
#
# This package is OPTIONAL. Lanory serves its own web UI on port 8787 and does not
# need LuCI at all; this package only adds a menu entry and a small status page
# inside LuCI for users who prefer to keep everything under one roof.
#
# It is built separately from luci (the LuCI SDK must be present):
#   make package/luci-app-lanory/compile
#
# The build was NOT executed in the development environment that produced this
# file. Verify it against the LuCI version that ships with your OpenWrt 25.12 /
# iStoreOS 25.12.5 release.

LUCI_TITLE:=LuCI support for Lanory
LUCI_DEPENDS:=+luci-base +lanory
LUCI_PKGARCH:=all

PKG_NAME:=luci-app-lanory
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_LICENSE:=MIT

# luci.mk provides LUCI_* handling and must be included after rules.mk. The
# conventional path is two directories up (the luci feed layout). When this
# package is dropped into an SDK as package/luci-app-lanory, adjust the path to
# $(TOPDIR)/feeds/luci/luci.mk, which is what the README documents.
include $(TOPDIR)/rules.mk
include ../../luci.mk

define Package/luci-app-lanory/description
 LuCI status and configuration page for Lanory (网绘). Optional: Lanory serves
 its own web UI on port 8787 and works without LuCI.
endef

# The postinst registers the Lanory UI as a reverse proxy on the LuCI port.
#
# Three things were established by testing against a real iStoreOS 25.12.5
# router, and each of them broke the first attempt:
#
#  1. The proxy prefix belongs on the existing "main" uhttpd section. A separate
#     uhttpd section with only proxy_prefix and no home/listen is ignored
#     entirely, and one that also binds :80 collides with main and kills uhttpd
#     ("Address in use" -> "No sockets bound, unable to continue").
#  2. `uci set uhttpd.@uhttpd[-1].home=/lanory` must not be used. Before
#     `uci add` has created a new section, @uhttpd[-1] IS the main section, so
#     this silently rewrites home from /www to /lanory and takes LuCI down until
#     the value is restored by hand.
#  3. home must not be set to a non-existent directory: uhttpd refuses to start
#     with "Invalid directory /lanory". A pure proxy needs no home at all.
#
# The change is additive and reversible: prerm removes the option again.
define Package/luci-app-lanory/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	. /lib/functions.sh
	config_load lanory
	local port
	config_get port main port 8787

	# Idempotent: only add the option when it is not already there.
	if ! uci -q get uhttpd.main.proxy_prefix >/dev/null 2>&1; then
		uci -q set uhttpd.main.proxy_prefix="/lanory=http://127.0.0.1:$port"
		uci commit uhttpd
		# Reload only when uhttpd is actually running; an image build has no
		# running uhttpd and the reload would fail harmlessly.
		[ -x /etc/init.d/uhttpd ] && /etc/init.d/uhttpd reload >/dev/null 2>&1
	fi

	# Flush the LuCI menu cache so the entry appears without a manual restart.
	rm -rf /tmp/luci-modulecache/* 2>/dev/null
}
exit 0
endef

define Package/luci-app-lanory/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	if uci -q get uhttpd.main.proxy_prefix >/dev/null 2>&1; then
		uci -q delete uhttpd.main.proxy_prefix
		uci commit uhttpd
		[ -x /etc/init.d/uhttpd ] && /etc/init.d/uhttpd reload >/dev/null 2>&1
	fi
}
exit 0
endef

# luci.mk does not call BuildPackage itself; every LuCI application ends with it.
$(eval $(call BuildPackage,luci-app-lanory))
