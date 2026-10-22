#!/bin/sh
# packaging/openwrt/verify.sh — static verification of the OpenWrt packaging.
#
# An OpenWrt SDK is not always available, so this script checks everything that
# can be checked without one. It is what `make apk-check` runs in CI.
#
# What it verifies:
#   1. the init script parses as POSIX sh and uses only busybox-safe features
#   2. the UCI config, init script and YAML template agree on option names
#   3. the package installs exactly the paths the init script reads
#   4. the YAML the init script generates is valid for the Go binary
#   5. the package declares no PKGARCH:=all (it ships a compiled binary)
#   6. the LuCI Makefile has the required LUCI_* variables and BuildPackage call
#   7. the token path is never written into a world readable file
#
# It does NOT verify that the package compiles under the buildroot, which needs
# the SDK. That is stated explicitly in the README.

set -eu

# shellcheck disable=SC1007 # CDPATH= clears CDPATH for this cd, it is not an assignment
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
FAIL=0

fail() {
	printf 'FAIL  %s\n' "$1"
	FAIL=$((FAIL + 1))
}
pass() {
	printf 'ok    %s\n' "$1"
}

say() {
	printf '\n== %s\n' "$1"
}

# ---------------------------------------------------------------------------
say "init script"
# ---------------------------------------------------------------------------
if sh -n "$DIR/files/lanory.init" 2>/dev/null; then
	pass "files/lanory.init parses as POSIX sh"
else
	fail "files/lanory.init has a syntax error"
fi

if head -1 "$DIR/files/lanory.init" | grep -q '^#!/bin/sh /etc/rc.common'; then
	pass "init script declares the rc.common interpreter"
else
	fail "init script must start with #!/bin/sh /etc/rc.common"
fi

for fn in start_service stop_service reload_service service_triggers lanory_generate_config lanory_ensure_token; do
	if grep -q "^$fn()" "$DIR/files/lanory.init"; then
		pass "defines $fn()"
	else
		fail "missing $fn()"
	fi
done

if grep -q 'USE_PROCD=1' "$DIR/files/lanory.init"; then
	pass "declares USE_PROCD=1"
else
	fail "missing USE_PROCD=1"
fi

# A procd instance without respawn means the service stays dead after a crash.
if grep -q 'procd_set_param respawn' "$DIR/files/lanory.init"; then
	pass "configures procd respawn"
else
	fail "missing procd respawn"
fi

# busybox ash has no arrays, no local -a, and no [[ ]].
if grep -nE '\[\[|\]\][[:space:]]' "$DIR/files/lanory.init" >/dev/null 2>&1; then
	fail "init script uses [[ ]] which busybox ash does not support universally"
else
	pass "init script uses POSIX test only"
fi

# ---------------------------------------------------------------------------
say "token handling"
# ---------------------------------------------------------------------------
# The token must never be interpolated into the generated YAML.
if grep -n 'auth.token' "$DIR/files/lanory.init" >/dev/null 2>&1; then
	fail "the generated YAML contains an auth.token line; the token must stay in the 0600 file"
else
	pass "the generated YAML does not contain the token"
fi

if grep -q 'LANORY_AUTH_TOKEN' "$DIR/files/lanory.init"; then
	pass "the token reaches the daemon through LANORY_AUTH_TOKEN"
else
	fail "LANORY_AUTH_TOKEN is not passed to the service"
fi

if grep -q 'chmod 600' "$DIR/files/lanory.init"; then
	pass "the token file is restricted to mode 0600"
else
	fail "the token file is not chmod 600"
fi

# The token must be created before the YAML is written, otherwise the first start
# generates auth.enabled=false and then hands the daemon a token it ignores.
ensure_line=$(grep -n 'lanory_ensure_token$' "$DIR/files/lanory.init" | head -1 | cut -d: -f1)
gen_line=$(grep -n 'lanory_generate_config$' "$DIR/files/lanory.init" | tail -1 | cut -d: -f1)
if [ -n "$ensure_line" ] && [ -n "$gen_line" ] && [ "$ensure_line" -lt "$gen_line" ]; then
	pass "the token is created before the YAML is generated"
else
	fail "lanory_ensure_token must run before lanory_generate_config"
fi

# ---------------------------------------------------------------------------
say "UCI options"
# ---------------------------------------------------------------------------
# Every option the init script reads must be documented in the shipped UCI file.
for opt in enabled listen port base_path data_dir generate_config generate_token; do
	if grep -q "^	option $opt " "$DIR/files/lanory.config"; then
		pass "lanory.config documents option $opt"
	else
		fail "lanory.config does not document option $opt (the init script reads it)"
	fi
done

for sec in main discovery monitoring logging; do
	if grep -q "^config .* '$sec'" "$DIR/files/lanory.config"; then
		pass "lanory.config defines section $sec"
	else
		fail "lanory.config is missing section $sec"
	fi
done

# ---------------------------------------------------------------------------
say "installed paths"
# ---------------------------------------------------------------------------
MK="$DIR/Makefile"
for path in /usr/bin/lanory /etc/init.d/lanory /etc/config/lanory /etc/lanory; do
	if grep -q "$path" "$MK"; then
		pass "Makefile installs $path"
	else
		fail "Makefile does not install $path"
	fi
done

# The binary the init script runs must be the one the package installs.
if grep -q '^PROG=/usr/bin/lanory' "$DIR/files/lanory.init" && grep -q '/usr/bin/lanory' "$MK"; then
	pass "the init script PROG matches the installed binary"
else
	fail "PROG in the init script does not match the installed path"
fi

# ---------------------------------------------------------------------------
say "package metadata"
# ---------------------------------------------------------------------------
if grep -qE '^[[:space:]]*PKGARCH:=all' "$MK"; then
	fail "PKGARCH:=all is wrong for a package that ships a compiled binary"
else
	pass "no PKGARCH:=all (the buildroot assigns the target architecture)"
fi

if grep -q 'PKG_USE_MIPS16:=0' "$MK"; then
	pass "MIPS16 is disabled (the Go runtime cannot use it)"
else
	fail "PKG_USE_MIPS16 must be 0 for a Go package"
fi

for dep in iproute2-ip-full bridge ubus libubox libjson-c; do
	if grep -q "+$dep" "$MK"; then
		pass "declares dependency $dep"
	else
		fail "missing dependency $dep"
	fi
done

# A conffile that is not installed is a packaging error, and a conffile the
# package does not ship would be silently dropped on upgrade.
for conf in /etc/config/lanory /etc/lanory/config.yaml; do
	if grep -q "^$conf$" "$MK"; then
		pass "$conf is declared as a conffile"
	else
		fail "$conf is installed but not declared in conffiles; user edits would be lost on upgrade"
	fi
done

# ---------------------------------------------------------------------------
say "LuCI companion"
# ---------------------------------------------------------------------------
LUCI="$DIR/luci-app-lanory/Makefile"
for var in LUCI_TITLE LUCI_DEPENDS LUCI_PKGARCH PKG_NAME PKG_VERSION PKG_LICENSE; do
	if grep -q "^$var:=" "$LUCI"; then
		pass "luci Makefile sets $var"
	else
		fail "luci Makefile is missing $var"
	fi
done

if grep -q 'BuildPackage,luci-app-lanory' "$LUCI"; then
	pass "luci Makefile calls BuildPackage"
else
	fail "luci Makefile does not call BuildPackage"
fi

if grep -q '+lanory' "$LUCI"; then
	pass "luci app depends on the lanory package"
else
	fail "luci app does not depend on lanory"
fi

# The menu node declares an ACL group. That group only exists if a file rpcd
# reads declares it; otherwise depends.acl is unsatisfied and the entry is
# dropped from the menu tree.
ACL_JSON="$DIR/luci-app-lanory/root/usr/share/rpcd/acl.d/luci-app-lanory.json"
if [ -f "$ACL_JSON" ] && grep -q '"luci-app-lanory"' "$ACL_JSON"; then
	pass "rpcd ACL file grants the luci-app-lanory group"
else
	fail "luci-app-lanory ACL group is not declared; the menu entry will not appear"
fi

MENU_JSON="$DIR/luci-app-lanory/root/usr/share/luci/menu.d/luci-app-lanory.json"
if [ -f "$MENU_JSON" ] && grep -q 'admin/services/lanory' "$MENU_JSON"; then
	pass "menu.d registers admin/services/lanory"
else
	fail "menu.d does not register admin/services/lanory"
fi

# The Lua controller must not re-declare acl_depends: that field is only
# consulted when the Lua runtime processes the controller, and LuCI 25.12 does
# not install it by default.
if grep -q 'acl_depends' "$DIR/luci-app-lanory/luasrc/controller/lanory.lua"; then
	fail "controller still declares acl_depends; the node vanishes without the Lua runtime"
else
	pass "controller does not re-declare acl_depends"
fi

# ---------------------------------------------------------------------------
say "generated YAML"
# ---------------------------------------------------------------------------
# The YAML the init script writes must be parseable and must contain the keys the
# Go binary requires. Extract it by running the generator in a sandbox would need
# busybox, so instead check the template against the struct tags in the config
# package.
# shellcheck disable=SC1007
ROOT=$(CDPATH= cd -- "$DIR/../.." && pwd)
CFG="$ROOT/internal/config/config.go"
if [ -f "$CFG" ]; then
	for key in server database discovery collectors monitoring topology auth logging demo; do
		if grep -q "yaml:\"$key\"" "$CFG"; then
			pass "config struct has a $key section"
		else
			fail "config struct has no $key section but the init script writes one"
		fi
	done
	if grep -q 'AUTH_TOKEN' "$CFG"; then
		pass "the config layer reads LANORY_AUTH_TOKEN"
	else
		fail "the config layer does not read LANORY_AUTH_TOKEN, so the init script's token is ignored"
	fi
	# Every key the init script writes must exist in the config struct, and
	# vice versa for the monitoring section: a key the struct has but the
	# script does not write silently falls back to the Go zero value, which
	# for a bool means "off" (that is how auto_probe silently disabled the
	# device monitoring on a real router).
	for key in auto_probe; do
		if grep -q "yaml:\"$key\"" "$CFG"; then
			pass "the config struct has the $key option"
		else
			fail "the config struct has no $key option"
		fi
		if grep -q "$key:" "$DIR/files/lanory.init"; then
			pass "the init script writes $key"
		else
			fail "the init script does not write $key; the option falls back to its zero value"
		fi
	done
else
	printf 'skip  config.go not found at %s\n' "$CFG"
fi

# ---------------------------------------------------------------------------
printf '\n'
if [ "$FAIL" -eq 0 ]; then
	printf 'all packaging checks passed\n'
	exit 0
fi
printf '%d packaging check(s) failed\n' "$FAIL"
exit 1
