# Lanory fixtures — sanitized copies
#
# The original EASEPi R1 snapshot contains real MAC addresses, IP addresses and
# hostnames from the author's home network. That snapshot is stored in
# `fixtures/private/` which is excluded from version control.
#
# The files in this directory are a de-identified copy with the exact same
# structural properties, so every parser test still exercises the same edge
# cases:
#
#   * a WAN port with a different subnet
#   * a bridge with three member ports, one of which has no carrier
#   * one port carrying several learned MACs (eth1)
#   * one port carrying a single MAC (eth2)
#   * multicast, self, permanent-local and zero MAC entries to be filtered
#   * docker0 and dummy0 virtual interfaces to be ignored
#   * a device only ever observed through IPv6 neighbours
#   * static-IP devices missing from the DHCP lease file
#
# Addresses were re-generated deterministically from the reserved documentation
# ranges (RFC 5737 for IPv4, RFC 3849 for IPv6) and locally administered MACs.
