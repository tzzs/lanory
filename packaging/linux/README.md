# Lanory on a generic Linux host (systemd)

This directory holds the systemd unit for a non-OpenWrt Linux host: Debian/Ubuntu
on a Raspberry Pi, an x86 mini PC used as a router, a NAS running a full
distribution. For OpenWrt / iStoreOS use `packaging/openwrt` instead; procd is not
systemd and the package manager is not apt.

**Status:** the unit file is complete and has been reviewed, but it was **not
installed or started** in the environment that produced it (that host is macOS).
`systemd-analyze verify` was not run. Verify it on the target before shipping.

## Install

```sh
# 1. Build the binary (static, no cgo).
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o lanory ./cmd/lanory

# 2. Install it.
sudo install -Dm0755 lanory /usr/bin/lanory

# 3. Create the service user and the data directory.
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lanory
sudo install -d -o lanory -g lanory -m 0750 /var/lib/lanory
sudo install -d -o root   -g lanory -m 0750 /etc/lanory

# 4. Configuration.
sudo install -m 0644 packaging/linux/config.yaml /etc/lanory/config.yaml
sudo chown root:lanory /etc/lanory/config.yaml
# Optional: a token for UI access from outside the LAN.
sudo sh -c 'head -c 32 /dev/urandom | base64 > /etc/lanory/token'
sudo chmod 0640 /etc/lanory/token && sudo chown root:lanory /etc/lanory/token

# 5. Unit.
sudo install -m 0644 packaging/linux/lanory.service /etc/systemd/system/lanory.service
sudo systemctl daemon-reload
sudo systemctl enable --now lanory
sudo systemctl status lanory
```

## Mirror of the procd behaviour

| Action | procd (OpenWrt) | systemd |
| --- | --- | --- |
| start | `/etc/init.d/lanory start` | `systemctl start lanory` |
| stop | `/etc/init.d/lanory stop` | `systemctl stop lanory` |
| restart | `/etc/init.d/lanory restart` | `systemctl restart lanory` |
| reload | `reload_service` (regenerate + restart) | `systemctl reload-or-restart lanory` |
| status | `/etc/init.d/lanory status` (procd + health) | `systemctl status lanory` |
| autostart | `/etc/init.d/lanory enable` | `systemctl enable lanory` |
| logs | `logread -e lanory` | `journalctl -u lanory -f` |
| data | `/var/lib/lanory` | `/var/lib/lanory` |

Differences that matter:

- the procd script generates `/etc/lanory/config.yaml` from UCI. There is no UCI
  on a generic Linux host, so this unit reads the YAML directly. Copy
  `packaging/linux/config.yaml` or write your own; the schema is in
  `docs/deployment.md`.
- systemd's sandboxing (`ProtectSystem=strict`, `ReadWritePaths=/var/lib/lanory`)
  is stricter than procd's. If you move the database, update `ReadWritePaths`.
- the unit grants `CAP_NET_RAW` only. Lanory never needs `CAP_NET_ADMIN`, because
  it must not be able to change interfaces.

## Uninstall

```sh
sudo systemctl disable --now lanory
sudo rm /etc/systemd/system/lanory.service
sudo systemctl daemon-reload
sudo rm -rf /usr/bin/lanory /etc/lanory
# Data is kept on purpose:
echo "database kept at /var/lib/lanory; remove it manually to purge"
```

## What is not verified

- that `systemd-analyze verify lanory.service` passes on your distribution's
  systemd version (`systemd-analyze` was not available here);
- that `ProtectSystem=strict` plus `ReadWritePaths` covers every path the binary
  writes (it should: the database is the only write target);
- that `CAP_NET_RAW` is enough for ICMP on a kernel with
  `net.ipv4.ping_group_range` restricted.
