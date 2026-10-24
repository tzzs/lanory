# luci-app-lanory — optional LuCI integration.
#
# This package is OPTIONAL. Lanory serves its own web UI on port 8787 and does not
# need LuCI at all; this package adds a menu entry, the shared application host and service settings
# inside LuCI for users who prefer to keep everything under one roof.
#
# It is built separately from luci (the LuCI SDK must be present):
#   make package/luci-app-lanory/compile
#
# The build was NOT executed in the development environment that produced this
# file. Verify it against the LuCI version that ships with your OpenWrt 25.12 /
# iStoreOS 25.12.5 release.
#
# Layout: root/ holds what is installed verbatim (the rpcd ACL group and the
# menu.d node), luasrc/ is installed by luci.mk under $(LUCI_LIBRARYDIR).

LUCI_TITLE:=LuCI support for Lanory
LUCI_DEPENDS:=+luci-base +luci-lua-runtime +curl +lanory
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
 LuCI shared application and service configuration for Lanory (网绘). Optional: Lanory serves
 its own web UI on port 8787 and works without LuCI.
endef

define Package/luci-app-lanory/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	# Flush the LuCI menu caches so the entry appears without a manual restart.
	rm -f /tmp/luci-indexcache* 2>/dev/null
	rm -rf /tmp/luci-modulecache/* 2>/dev/null
}
exit 0
endef

define Package/luci-app-lanory/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	rm -f /tmp/luci-indexcache* 2>/dev/null
	rm -rf /tmp/luci-modulecache/* 2>/dev/null
}
exit 0
endef

# luci.mk does not call BuildPackage itself; every LuCI application ends with it.
$(eval $(call BuildPackage,luci-app-lanory))
