package monitoring

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// Link health is a separate concern from device reachability, and conflating
// them produces wrong topology graphs:
//
//   - a device can be REACHABLE while its port is DOWN (wireless association,
//     another host on the same broadcast domain);
//   - a port can be UP while the device behind it is UNREACHABLE (the peer is
//     asleep, firewalled, or simply not answering).
//
// This module therefore only reports physical/link facts (carrier, oper_state,
// negotiated speed, counters) and the transitions between them. It never
// derives a device state from a link state and never derives a link state from a
// probe result.

// sysNetPath is the Linux sysfs tree holding interface state. The service also
// runs on macOS during development, where the path does not exist; every read is
// best effort and a missing file is reported as "unknown", never as "down".
const sysNetPath = "/sys/class/net"

// LinkObserver polls interface state, computes model.EdgeStatus and emits
// link.up / link.down / speed.changed transitions.
type LinkObserver struct {
	store Store
	bus   Publisher

	// sysRoot overrides /sys/class/net. Tests point it at a synthetic tree.
	sysRoot string

	mu      sync.Mutex
	last    map[string]linkSample
	events  []model.NetworkEvent
	linkErr error
}

// linkEventBuffer bounds how many link events the observer retains in memory.
const linkEventBuffer = 256

// linkSample is the last observed state of one interface.
type linkSample struct {
	// carrier is the physical link state read from sysfs carrier.
	carrier bool
	// oper is the kernel oper_state.
	oper string
	// speed is the speed observed in this sample; 0 when the kernel reports
	// none (which it does while a link is down).
	speed int
	// lastSpeed is the last non-zero speed seen for the interface. It exists so
	// a link that goes down and comes back up at a different speed is detected
	// as a speed change rather than silently forgetting the old value.
	lastSpeed int
	seenAt    time.Time
}

// NewLinkObserver returns an observer reading the real /sys/class/net tree.
func NewLinkObserver(store Store, bus Publisher) *LinkObserver {
	return &LinkObserver{
		store:   store,
		bus:     bus,
		sysRoot: sysNetPath,
		last:    map[string]linkSample{},
	}
}

// newLinkObserverWithRoot returns an observer reading a synthetic sysfs tree.
// Only used by tests.
func newLinkObserverWithRoot(root string, store Store, bus Publisher) *LinkObserver {
	o := NewLinkObserver(store, bus)
	o.sysRoot = root
	return o
}

// SetSysRoot overrides the sysfs root. Exposed for tests that cannot create
// files before constructing the observer.
func (o *LinkObserver) SetSysRoot(root string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sysRoot = root
}

// LinkStatus is the runtime status of one interface.
type LinkStatus struct {
	Interface  model.NetworkInterface `json:"interface"`
	Status     model.EdgeStatus       `json:"status"`
	Reason     string                 `json:"reason,omitempty"`
	ReasonCode *model.Reason          `json:"reason_code,omitempty"`
}

// Observe polls the interface state once and returns the status of every
// interface it could read. Transitions detected during the poll are recorded as
// model.NetworkEvent and published on the event bus.
func (o *LinkObserver) Observe() ([]LinkStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	names, err := o.listInterfaces()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]LinkStatus, 0, len(names))
	for _, name := range names {
		sample, ok := o.readSample(name)
		if !ok {
			continue
		}
		prev, had := o.last[name]
		if had && sample.speed == 0 {
			sample.lastSpeed = prev.lastSpeed
		} else {
			sample.lastSpeed = sample.speed
		}
		status, reason := edgeStatusFor(sample)

		iface := model.NetworkInterface{
			Name:      name,
			OperState: model.OperState(sample.oper),
			Carrier:   sample.carrier,
			SpeedMbps: sample.speed,
			LastSeen:  now,
		}
		// Fall back to the store's last known row so the API keeps returning
		// stable metadata (id, role, type, counters) for the interface.
		if o.store != nil {
			if dev, err := o.selfDeviceID(); err == nil {
				if stored, err := o.store.GetInterfaceByName(dev, name); err == nil {
					iface.ID = stored.ID
					iface.DeviceID = stored.DeviceID
					iface.MAC = stored.MAC
					iface.Type = stored.Type
					iface.Role = stored.Role
					iface.AdminState = stored.AdminState
					iface.MTU = stored.MTU
					iface.BridgeID = stored.BridgeID
					iface.VLANID = stored.VLANID
					iface.IsPhysical = stored.IsPhysical
					iface.RXBytes = stored.RXBytes
					iface.TXBytes = stored.TXBytes
					iface.RXErrors = stored.RXErrors
					iface.TXErrors = stored.TXErrors
					iface.RXPackets = stored.RXPackets
					iface.TXPackets = stored.TXPackets
					iface.RXDropped = stored.RXDropped
					iface.TXDropped = stored.TXDropped
					iface.LastSpeedChange = stored.LastSpeedChange
				}
			}
		}

		if had {
			o.detectTransitions(name, prev, sample, now)
		}
		o.last[name] = sample
		out = append(out, LinkStatus{Interface: iface, Status: status, Reason: reason, ReasonCode: edgeReasonCodeFor(sample)})
	}
	return out, nil
}

// selfDeviceID returns the id of the device Lanory runs on. The store keeps
// interfaces keyed by device, so the observer needs it to find its own rows.
func (o *LinkObserver) selfDeviceID() (string, error) {
	if o.store == nil {
		return "", fmt.Errorf("monitoring: no store")
	}
	ifaces, err := o.store.ListAllInterfaces()
	if err != nil {
		return "", err
	}
	if len(ifaces) > 0 {
		return ifaces[0].DeviceID, nil
	}
	return "", fmt.Errorf("monitoring: no interfaces stored yet")
}

// detectTransitions emits link.up / link.down / speed.changed for a sample that
// differs from the previous one.
//
// Speed is compared against the last KNOWN speed rather than the previous raw
// sample: when a link goes down the kernel reports speed -1 (or 0), which is not
// a speed and must not be treated as a downgrade to 1 Mbps.
func (o *LinkObserver) detectTransitions(name string, prev, cur linkSample, now time.Time) {
	carrierChanged := prev.carrier != cur.carrier
	speedChanged := cur.lastSpeed > 0 && prev.lastSpeed > 0 && cur.lastSpeed != prev.lastSpeed

	if carrierChanged {
		ev := model.NetworkEvent{
			EventType: model.EventLinkUp,
			OldValue:  carrierLabel(prev.carrier),
			NewValue:  carrierLabel(cur.carrier),
			Message:   fmt.Sprintf("%s carrier %s -> %s", name, carrierLabel(prev.carrier), carrierLabel(cur.carrier)),
			ReasonCode: &model.Reason{
				Code:   model.ReasonLinkCarrierChanged,
				Params: map[string]string{"interface": name, "old": carrierLabel(prev.carrier), "new": carrierLabel(cur.carrier)},
			},
			Evidence:  fmt.Sprintf("sysfs: /sys/class/net/%s/carrier", name),
			Timestamp: now,
		}
		if !cur.carrier {
			ev.EventType = model.EventLinkDown
		}
		o.recordEvent(name, ev)
	}
	if speedChanged {
		ev := model.NetworkEvent{
			EventType: model.EventSpeedChanged,
			OldValue:  strconv.Itoa(prev.lastSpeed),
			NewValue:  strconv.Itoa(cur.lastSpeed),
			Message:   fmt.Sprintf("%s negotiated speed %d Mbps -> %d Mbps", name, prev.lastSpeed, cur.lastSpeed),
			ReasonCode: &model.Reason{
				Code:   model.ReasonLinkSpeedChanged,
				Params: map[string]string{"interface": name, "old_mbps": strconv.Itoa(prev.lastSpeed), "new_mbps": strconv.Itoa(cur.lastSpeed)},
			},
			Evidence:  fmt.Sprintf("sysfs: /sys/class/net/%s/speed", name),
			Timestamp: now,
		}
		o.recordEvent(name, ev)
	}
}

// recordEvent attaches the owning device, retains, persists and publishes a link
// event.
//
// A link event is evidence about the device whose port changed — the router
// itself, in the MVP — so it is attributed to that device. Only when no owner can
// be resolved (a fresh install before discovery has run, or a host without
// sysfs) does the event stay device-less; it is then kept in memory and
// published on the bus, and the store error is recorded instead of hidden.
func (o *LinkObserver) recordEvent(iface string, ev model.NetworkEvent) {
	if ev.DeviceID == "" {
		ev.DeviceID = o.ownerDeviceID(iface)
	}
	o.events = append(o.events, ev)
	if len(o.events) > linkEventBuffer {
		o.events = o.events[len(o.events)-linkEventBuffer:]
	}
	if o.store != nil {
		if err := o.store.SaveEvent(&ev); err != nil {
			o.linkErr = err
		}
	}
	if o.bus != nil {
		o.bus.PublishLinkUpdated(nil, ev.Message)
	}
}

// ownerDeviceID resolves the device that owns an interface.
func (o *LinkObserver) ownerDeviceID(iface string) string {
	if o.store == nil {
		return ""
	}
	ifaces, err := o.store.ListAllInterfaces()
	if err != nil {
		return ""
	}
	for _, i := range ifaces {
		if i.Name == iface && i.DeviceID != "" {
			return i.DeviceID
		}
	}
	return ""
}

// Events returns the link events emitted since the observer was created.
//
// Events are retained in memory because the current schema cannot persist an
// event without a device id. They are still published on the SSE bus, so the UI
// sees them in real time.
func (o *LinkObserver) Events() []model.NetworkEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]model.NetworkEvent, len(o.events))
	copy(out, o.events)
	return out
}

// LinkError returns the last error hit while persisting a link event, if any.
func (o *LinkObserver) LinkError() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.linkErr
}

// --- sysfs reading ---------------------------------------------------------

// listInterfaces returns the interface names present in the sysfs tree.
//
// sysfs interface entries are symlinks to directories; readSample follows them
// and rejects anything that is not a directory.
func (o *LinkObserver) listInterfaces() ([]string, error) {
	entries, err := os.ReadDir(o.sysRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// macOS / container without sysfs: nothing to observe, and that is
			// not an error.
			return nil, nil
		}
		return nil, fmt.Errorf("monitoring: read %s: %w", o.sysRoot, err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		// Skip loopback and the virtual interfaces the kernel creates for
		// tunnels, dummy devices and container bridges. They are not network
		// ports, they never carry a household device, and listing them made the
		// monitoring page show eighteen rows of which eleven were unmanaged:
		// docker0, dummy0, gre0, gretap0, erspan0, ip6gre0, ip6tnl0, sit0,
		// teql0, tunl0 and lo.
		if parsers.LooksVirtualByName(name) {
			continue
		}
		// A name is not enough: iStoreOS creates "dheth0", a veth device for
		// its DHCP helper, and no name-based rule can tell that apart from a
		// physical port. The kernel does say so, though, in two ways: a veth
		// whose peer is in another network namespace has a "link-netns"
		// attribute, and a software device lives under
		// /sys/devices/virtual/net rather than under a platform device.
		//
		// A bridge is also under /sys/devices/virtual, but it is a real
		// forwarding device built from physical ports, so it stays.
		if o.isVirtualDevice(filepath.Join(o.sysRoot, name), name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// isVirtualDevice reports whether a sysfs interface entry is not a real port.
//
// Two kernel attributes give it away:
//
//   - "link-netns" is present on a veth whose peer lives in another network
//     namespace. iStoreOS creates such a device ("dheth0") for its DHCP
//     helper, and it is bridge member without being a port a user can plug
//     something into.
//   - "type" names the device kind for the pseudo devices the kernel creates
//     (dummy, tunl, gretap and so on).
//
// A device without either attribute is a real interface.
func (o *LinkObserver) isVirtualDevice(dir, name string) bool {
	// A veth whose peer lives in another network namespace.
	if _, ok := readSysfsString(dir, "link-netns"); ok {
		return true
	}
	// The kernel reports the device kind for the pseudo devices.
	if kind, ok := readSysfsString(dir, "type"); ok {
		switch kind {
		case "dummy", "tunl", "gre", "gretap", "erspan", "ip6gre", "ip6gretap",
			"sit", "ipip", "ip6tnl", "vti", "vlan", "macvlan", "macvtap",
			"vxlan", "geneve", "wireguard", "ifb", "teql", "veth":
			return true
		}
	}
	// A software device lives under /sys/devices/virtual/net. A bridge does
	// too, but it is a real forwarding device, so it is kept.
	if o.isUnderVirtualDir(name) && !isBridgeName(name) {
		return true
	}
	return false
}

// isUnderVirtualDir reports whether the sysfs entry for an interface lives
// under /sys/devices/virtual/net.
//
// The sysfs root used by the observer may be a test directory, in which case
// there is no way to tell, and the answer is false.
func (o *LinkObserver) isUnderVirtualDir(name string) bool {
	real, err := filepath.EvalSymlinks(filepath.Join(o.sysRoot, name))
	if err != nil {
		return false
	}
	return strings.Contains(real, "/devices/virtual/net/")
}

// isBridgeName reports whether an interface name is a bridge.
func isBridgeName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "br" || strings.HasPrefix(n, "br-") || strings.HasPrefix(n, "br.")
}

// readSample reads carrier, operstate and speed for one interface.
//
// A missing or unparsable file yields a zero value rather than an error: the
// interface is then reported as unverified instead of down.
func (o *LinkObserver) readSample(name string) (linkSample, bool) {
	dir := filepath.Join(o.sysRoot, name)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return linkSample{}, false
	}
	s := linkSample{seenAt: time.Now().UTC()}
	if v, ok := readSysfsString(dir, "carrier"); ok {
		s.carrier = v == "1"
	}
	if v, ok := readSysfsString(dir, "operstate"); ok {
		s.oper = v
	}
	if v, ok := readSysfsString(dir, "speed"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			s.speed = n
		}
	}
	return s, true
}

// readSysfsString reads one sysfs attribute, reporting whether it existed.
func readSysfsString(dir, attr string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, attr))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// edgeStatusFor derives model.EdgeStatus from a link sample.
//
//	carrier up             -> link_up
//	carrier down           -> link_down
//	no carrier information -> unverified (never claim a broken cable)
//
// oper_state is only consulted when carrier says nothing, because carrier
// reflects the physical link while oper_state reflects the kernel view.
func edgeReasonCodeFor(s linkSample) *model.Reason {
	return model.LinkReasonCode(s.carrier, model.OperState(s.oper), s.speed)
}

func edgeStatusFor(s linkSample) (model.EdgeStatus, string) {
	return model.LinkStatusFor(s.carrier, model.OperState(s.oper), s.speed)
}

// carrierLabel renders a carrier value for event messages.
func carrierLabel(carrier bool) string {
	if carrier {
		return "up"
	}
	return "down"
}
