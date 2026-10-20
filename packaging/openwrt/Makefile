# Lanory — OpenWrt / iStoreOS package
#
# This is a *new* package. It is not in any official OpenWrt feed, and it is not
# in the packages feed either; you build it from this tree against an OpenWrt SDK.
#
# The build was NOT executed in the development environment that produced this
# file (no OpenWrt SDK, no cross toolchain, macOS host). Everything below is the
# standard a package must follow, written to be reviewable: read the README in
# this directory before trusting it, and verify the target SDK/toolchain against
# the actual OpenWrt 25.12 / iStoreOS 25.12.5 release you are building for.
#
# Layout produced by the build:
#   /usr/bin/lanory
#   /etc/init.d/lanory
#   /etc/config/lanory          (UCI, created on first install)
#   /etc/lanory/config.yaml     (generated from UCI on start; see init script)
#   /var/lib/lanory/            (database, preserved across upgrades)
#
# The package name is "lanory"; the optional LuCI companion is
# "luci-app-lanory" and lives in ./luci-app-lanory/Makefile.

include $(TOPDIR)/rules.mk

PKG_NAME:=lanory
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_MAINTAINER:=Lanory contributors
PKG_HOMEPAGE:=https://github.com/lanory/lanory

# The Go build is done by the Go package infrastructure (golang.mk), which is
# what OpenWrt uses for Go packages. It cross compiles with the target
# toolchain, so no cgo and no host Go module cache are involved.
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

# The upstream source lives one level above this Makefile (the repository root).
# When building from a tarball, set PKG_SOURCE/PKG_SOURCE_URL instead.
PKG_SOURCE_PROTO:=git
PKG_SOURCE_URL:=https://github.com/lanory/lanory.git
PKG_SOURCE_VERSION:=HEAD
PKG_SOURCE_SUBDIR:=$(PKG_NAME)-$(PKG_VERSION)

GO_PKG:=github.com/lanory/lanory
GO_PKG_BUILD_PKG:=github.com/lanory/lanory/cmd/lanory
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)
GO_PKG_TAGS:=netgo,osusergo

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/golang.mk

# Runtime dependencies.
#
# Lanory reads the router's own state, so the commands and files it depends on
# must be present:
#   - iproute2-ip-full: `ip -j link`, `ip -j neigh`, `ip -j addr`
#   - bridge:           `bridge -j fdb show`
#   - ubus + libubox:   `ubus call network.interface dump` (OpenWrt 21+ uses ubus)
#   - jshn:             ubus JSON helpers used by the init script fallback
#   - ca-bundle:        HTTPS to an upstream modem / vendor API
#   - nftables (or iptables): only for `nft list ruleset` in the DMZ detector;
#                       the package degrades gracefully when absent
# It deliberately does NOT depend on: dnsmasq (leases are read as a file when
# present), curl, node, python.
define Package/lanory
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Network Monitoring
  TITLE:=Home network topology visualisation and monitoring
  URL:=https://github.com/lanory/lanory
  DEPENDS:=+libc +iproute2-ip-full +bridge +ubus +libubox +libjson-c +ca-bundle
  PROVIDES:=lanory
  CONFLICTS:=luci-app-lanory-web
  PKGARCH:=all
endef

define Package/lanory/description
 Lanory (网绘) is a self-hosted home network topology visualisation and
 monitoring tool for OpenWrt and iStoreOS. It discovers devices from the
 router's own bridge FDB, ARP/NDP neighbours and DHCP leases, draws an
 interactive topology, and monitors reachability over time.

 Read-only by default: it never modifies WAN/LAN/DHCP/firewall/DNS/routes.
endef

define Package/lanory/conffiles
/etc/config/lanory
/etc/lanory/config.yaml
endef

# The Go build produces bin/lanory; install it plus the init script and the
# config directory. Data lives in /var/lib/lanory and is created at runtime.
define Package/lanory/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/lanory $(1)/usr/bin/lanory
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/lanory.init $(1)/etc/init.d/lanory
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/lanory.config $(1)/etc/config/lanory
	$(INSTALL_DIR) $(1)/etc/lanory
	$(INSTALL_CONF) ./files/config.yaml $(1)/etc/lanory/config.yaml
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/99-lanory $(1)/etc/uci-defaults/99-lanory
	$(INSTALL_DIR) $(1)/usr/share/lanory
	$(INSTALL_DATA) ./files/README.deploy.md $(1)/usr/share/lanory/README.md
	$(INSTALL_DIR) $(1)/var/lib/lanory
endef

# Prerm/postinst preserve /var/lib/lanory across upgrades. See files/.
define Package/lanory/preinst
#!/bin/sh
# Nothing to do before install: the upgrade path preserves data via postinst.
exit 0
endef

define Package/lanory/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	# Enable autostart on first install, but only if the user has not disabled it.
	if [ ! -f /etc/config/lanory ] || ! uci -q get lanory.main.enabled >/dev/null 2>&1; then
		uci -q batch <<-EOF >/dev/null 2>&1
			set lanory.main=lanory
			set lanory.main.enabled='1'
			set lanory.main.data_dir='/var/lib/lanory'
			commit lanory
		EOF
	fi
	/etc/init.d/lanory enable
	/etc/init.d/lanory restart >/dev/null 2>&1
}
exit 0
endef

define Package/lanory/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	/etc/init.d/lanory stop >/dev/null 2>&1
	/etc/init.d/lanory disable >/dev/null 2>&1
}
exit 0
endef

define Package/lanory/postrm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	# /var/lib/lanory is deliberately NOT removed here: an uninstall must not
	# destroy the user's history. `lanory purge` (or removing the directory by
	# hand) is the explicit way to delete data. See README.md.
	if [ -d /var/lib/lanory ]; then
		echo "Lanory data kept in /var/lib/lanory (remove it manually to purge)."
	fi
}
exit 0
endef

$(eval $(call BuildPackage,lanory))
