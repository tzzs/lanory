# Lanory (deployed by the OpenWrt package)

This note is installed to `/usr/share/lanory/README.md` so it is available on the
device without a network connection.

## Quick start

```sh
/etc/init.d/lanory enable     # start on boot
/etc/init.d/lanory start      # start now
/etc/init.d/lanory status     # procd + health check
logread -e lanory             # service log
```

Open `http://<router-ip>:8787/`.

## Configuration

- `/etc/config/lanory` — UCI, the file you edit.
- `/etc/lanory/config.yaml` — generated from UCI on every start.
- `/var/lib/lanory/lanory.db` — the database. Preserved on upgrade and uninstall.

To use a hand-written YAML instead, set `lanory.main.generate_config='0'` and edit
`/etc/lanory/config.yaml` directly.

To require a token when the UI is reachable from outside the LAN:

```sh
# prefer the file over the YAML so the secret is not world readable
echo 'a-long-random-token' > /etc/lanory/token
chmod 600 /etc/lanory/token
uci set lanory.main.enabled='1'
uci commit lanory
/etc/init.d/lanory restart
```

## What Lanory reads

- `ip -j link`, `ip -j addr`, `ip -j neigh`, `ip -j -6 neigh`
- `bridge -j fdb show`
- `ubus call network.interface dump`, `ubus call network.device status`
- `/tmp/dhcp.leases` (when present)

## What Lanory never does

It never writes to `/etc/config/network`, `dhcp`, `firewall`, `dnsmasq`, the
routing table, or the WAN/LAN configuration. Discovery is read-only; the optional
active scan (disabled by default) only probes subnets you explicitly authorise.

## Purge

```sh
/etc/init.d/lanory stop
/etc/init.d/lanory disable
apk del lanory          # or: opkg remove lanory
rm -rf /var/lib/lanory  # explicit data purge
```
