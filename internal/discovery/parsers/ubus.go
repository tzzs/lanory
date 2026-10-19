package parsers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readFile reads a fixture. It exists as a seam so the fixture location can be
// overridden by the tests without touching os directly everywhere.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// ErrNotSupported marks input that is structurally unusable.
var ErrNotSupported = errors.New("parsers: unsupported input")

// UbusDevice is one entry of `ubus call network.device dump`.
type UbusDevice struct {
	Name    string
	Up      bool
	Link    bool
	MTU     int
	MAC     string
	Type    string
	Speed   int
	Members []string
	Stats   UbusStats
}

// UbusStats holds the interface counters.
type UbusStats struct {
	RXBytes   uint64
	TXBytes   uint64
	RXPackets uint64
	TXPackets uint64
	RXErrors  uint64
	TXErrors  uint64
	RXDropped uint64
	TXDropped uint64
}

// rawDevice mirrors the JSON shape of one network.device dump entry.
type rawDevice struct {
	Up            bool           `json:"up"`
	Link          bool           `json:"link"`
	MTU           int            `json:"mtu"`
	MACAddr       string         `json:"macaddr"`
	Type          string         `json:"type"`
	Speed         int            `json:"speed"`
	BridgeMembers []string       `json:"bridge-members"`
	Stats         map[string]any `json:"stats"`
}

// UbusDeviceDump is the parsed form of `ubus call network.device dump`.
type UbusDeviceDump struct {
	Devices map[string]*UbusDevice
}

// ParseUbusDeviceDump parses the JSON emitted by `ubus call network.device dump`.
func ParseUbusDeviceDump(data []byte) (*UbusDeviceDump, error) {
	var raw map[string]rawDevice
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsers: ubus network.device dump: %w", err)
	}
	out := &UbusDeviceDump{Devices: make(map[string]*UbusDevice, len(raw))}
	for name, d := range raw {
		out.Devices[name] = &UbusDevice{
			Name:    name,
			Up:      d.Up,
			Link:    d.Link,
			MTU:     d.MTU,
			MAC:     NormalizeMAC(d.MACAddr),
			Type:    d.Type,
			Speed:   d.Speed,
			Members: d.BridgeMembers,
			Stats:   parseStats(d.Stats),
		}
	}
	return out, nil
}

func parseStats(m map[string]any) UbusStats {
	var s UbusStats
	if m == nil {
		return s
	}
	s.RXBytes = uint64(toInt(m["rx_bytes"]))
	s.TXBytes = uint64(toInt(m["tx_bytes"]))
	s.RXPackets = uint64(toInt(m["rx_packets"]))
	s.TXPackets = uint64(toInt(m["tx_packets"]))
	s.RXErrors = uint64(toInt(m["rx_errors"]))
	s.TXErrors = uint64(toInt(m["tx_errors"]))
	s.RXDropped = uint64(toInt(m["rx_dropped"]))
	s.TXDropped = uint64(toInt(m["tx_dropped"]))
	return s
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		return ParseUint(n)
	}
	return 0
}

// UbusInterfaceAddress is one address on a logical interface.
type UbusInterfaceAddress struct {
	Address string
	Mask    int
}

// UbusRoute is one route on a logical interface.
type UbusRoute struct {
	Target  string
	Mask    int
	Nexthop string
	Metric  int
}

// UbusInterface is one logical interface from `ubus call network.interface dump`.
type UbusInterface struct {
	Name       string
	Up         bool
	Available  bool
	Pending    bool
	Device     string
	L3Device   string
	Proto      string
	Uptime     int
	IPv4       []UbusInterfaceAddress
	IPv6       []UbusInterfaceAddress
	Routes     []UbusRoute
	DNSServers []string
}

// rawInterface mirrors the JSON shape of one network.interface dump entry.
type rawInterface struct {
	Up         bool             `json:"up"`
	Available  bool             `json:"available"`
	Pending    bool             `json:"pending"`
	Device     string           `json:"device"`
	L3Device   string           `json:"l3_device"`
	Proto      string           `json:"proto"`
	Uptime     int              `json:"uptime"`
	IPv4Addr   []map[string]any `json:"ipv4-address"`
	IPv6Addr   []map[string]any `json:"ipv6-address"`
	Routes     []map[string]any `json:"route"`
	DNSServers []string         `json:"dns-server"`
}

// UbusInterfaceDump is the parsed form of `ubus call network.interface dump`.
type UbusInterfaceDump struct {
	Interfaces map[string]*UbusInterface
}

// rawInterfaceDump is the outer shape of the network.interface dump. Depending
// on the OpenWrt/netifd version the logical interfaces live either directly at
// the top level or nested under an "interface" key.
type rawInterfaceDump struct {
	Interfaces map[string]rawInterface `json:"interface"`
}

// ParseUbusInterfaceDump parses the JSON emitted by
// `ubus call network.interface dump`.
func ParseUbusInterfaceDump(data []byte) (*UbusInterfaceDump, error) {
	var raw rawInterfaceDump
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsers: ubus network.interface dump: %w", err)
	}
	out := &UbusInterfaceDump{Interfaces: make(map[string]*UbusInterface, len(raw.Interfaces))}
	for name, i := range raw.Interfaces {
		iface := &UbusInterface{
			Name:       name,
			Up:         i.Up,
			Available:  i.Available,
			Pending:    i.Pending,
			Device:     i.Device,
			L3Device:   i.L3Device,
			Proto:      i.Proto,
			Uptime:     i.Uptime,
			IPv4:       parseAddrs(i.IPv4Addr),
			IPv6:       parseAddrs(i.IPv6Addr),
			DNSServers: i.DNSServers,
		}
		for _, r := range i.Routes {
			iface.Routes = append(iface.Routes, UbusRoute{
				Target:  str(r["target"]),
				Mask:    toInt(r["mask"]),
				Nexthop: str(r["nexthop"]),
				Metric:  toInt(r["metric"]),
			})
		}
		out.Interfaces[name] = iface
	}
	return out, nil
}

func parseAddrs(in []map[string]any) []UbusInterfaceAddress {
	out := make([]UbusInterfaceAddress, 0, len(in))
	for _, a := range in {
		out = append(out, UbusInterfaceAddress{
			Address: str(a["address"]),
			Mask:    toInt(a["mask"]),
		})
	}
	return out
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// DefaultGateway returns the IPv4 default route nexthop of an interface, or ""
// when the interface carries no default route.
func (i *UbusInterface) DefaultGateway() string {
	for _, r := range i.Routes {
		if r.Target == "0.0.0.0" && r.Mask == 0 && r.Nexthop != "" {
			return r.Nexthop
		}
	}
	return ""
}

// Subnets returns the CIDR list of the interface's IPv4 addresses.
func (i *UbusInterface) Subnets() []string {
	var out []string
	for _, a := range i.IPv4 {
		if a.Address == "" {
			continue
		}
		out = append(out, CIDR(a.Address, a.Mask))
	}
	return out
}

// CIDR renders address/prefix, falling back to /32 for an unusable mask.
func CIDR(address string, mask int) string {
	if mask < 0 || mask > 128 {
		mask = 32
	}
	return address + "/" + strconv.Itoa(mask)
}

// InterfaceTypeFromUbus maps a ubus device type onto the model enum.
func InterfaceTypeFromUbus(t string) (out string) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "bridge":
		return "bridge"
	case "loopback":
		return "loopback"
	case "vlan":
		return "vlan"
	case "dummy":
		return "virtual"
	case "tunnel":
		return "virtual"
	case "wireless":
		return "wireless"
	case "ethernet":
		return "ethernet"
	}
	return "other"
}
