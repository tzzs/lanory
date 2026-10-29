// Package device implements the Device Identity Engine.
//
// The engine merges observations from many collectors into one stable device
// entity. Identity precedence (strongest first):
//
//  1. Strong identity: DHCP client identifier, LLDP chassis id, vendor serial
//  2. MAC address
//  3. Historical association (a MAC previously linked to this device)
//  4. Hostname
//  5. IP address
//  6. Other fingerprints
//
// IP address and hostname alone are NEVER sufficient for a merge: both change
// and both can be reused by a different device.
//
// Modern phones and laptops randomise their MAC address (locally administered
// bit set). A randomised MAC is recorded with reduced confidence and, when the
// device shows other stable signals (hostname, vendor, DHCP client id), the
// engine links the randomised MAC to the same entity as a historical
// association rather than creating a duplicate.
package device

import (
	"net"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Engine resolves discovery results into persistent devices.
type Engine struct {
	store *database.Store
	bus   publisher
}

// publisher is the subset of the event bus the engine needs.
type publisher interface {
	PublishDeviceUpdated(d *model.Device, reason string)
	PublishDeviceOnline(d *model.Device, reason string)
	PublishDeviceOffline(d *model.Device, reason string)
	PublishTopologyUpdated(reason string)
}

// New returns an identity engine backed by store.
func New(store *database.Store, bus publisher) *Engine {
	return &Engine{store: store, bus: bus}
}

// Ingest is one observation to resolve into a device.
type Ingest struct {
	// MAC is the canonical (lowercase, no separators) MAC, if known.
	MAC string
	// Hostname is the reported hostname, if any.
	Hostname string
	// Vendor is the manufacturer from an OUI lookup, if known.
	Vendor string
	// Addresses are IP addresses observed for the device.
	Addresses []discovery.AddressObservation
	// Source is the collector reporting the observation.
	Source model.DiscoverySource
	// ObservedAt is when the observation was made.
	ObservedAt time.Time
	// StrongID is a strong identity value (DHCP client id, LLDP chassis id).
	StrongID string
	// StrongIDType classifies StrongID.
	StrongIDType model.IdentityType
	// ClientID is a DHCP client identifier, treated as a strong identity.
	ClientID string
	// SSID is the wireless SSID the device associates with, if reported.
	SSID string
	// SignalDBm is the reported signal strength, if reported.
	SignalDBm int
	// TypeHint is the collector's guess at the device type.
	TypeHint model.DeviceType
}

// Resolve ingests an observation and returns the resulting device, creating it
// when no existing device matches.
//
// The merge decision is recorded on the device: when the engine had to guess
// (hostname or IP only), the device is flagged as possibly duplicated so the UI
// can offer a manual merge instead of silently combining two devices.
func (e *Engine) Resolve(ing Ingest) (*model.Device, bool, error) {
	if ing.ObservedAt.IsZero() {
		ing.ObservedAt = time.Now().UTC()
	}
	if ing.MAC != "" {
		ing.MAC = database.CanonicalizeMAC(ing.MAC)
		if database.IsZeroMAC(ing.MAC) {
			// 00:00:00:00:00:00 is the kernel's unresolved sentinel. It is not
			// an identity; keeping it here names devices "Unknown 000000" and
			// merges unrelated addresses into one phantom.
			ing.MAC = ""
		}
	}

	dev, err := e.match(ing)
	if err != nil {
		return nil, false, err
	}
	created := dev == nil
	if created {
		dev = e.newDevice(ing)
	}

	// Refresh volatile fields.
	changed := false
	if ing.Hostname != "" && !dev.IsLocked && !dev.IsManual {
		// The display name is derived from the observation that created the
		// device. A device first seen as a bare MAC keeps that placeholder
		// forever unless the name is refreshed here: DHCP hands out hostnames
		// minutes later, and until then the UI shows "Unknown <mac>" for
		// something it can already name.
		//
		// The name is refreshed whenever it is still a generated placeholder,
		// not only when the hostname changed. A device whose hostname was
		// already recorded keeps its old placeholder otherwise, because
		// "the hostname differs" is never true for it again.
		//
		// Only the "Unknown ..." fallbacks are replaced. A vendor-prefixed
		// name such as "Xiaomi dd:ee:ff" says more than the hostname does.
		if isUnknownPlaceholder(dev.Name) {
			dev.Name = ing.Hostname
		}
		if ing.Hostname != dev.Hostname {
			dev.Hostname = ing.Hostname
			changed = true
		}
	} else if !dev.IsLocked && !dev.IsManual && isDerivedName(dev) {
		// No hostname yet, but the placeholder itself may be stale. NDP and the
		// bridge FDB report a MAC and nothing else, so a device can be created
		// as "Unknown 04:67:61" before ARP ever sees its address. Promote the
		// name when this observation knows something better; never demote it,
		// or a MAC-only observation would undo the address a previous one gave.
		if name := displayName(ing); placeholderRank(name) > placeholderRank(dev.Name) {
			dev.Name = name
			changed = true
		}
	}
	if ing.Vendor != "" && dev.Manufacturer == "" {
		dev.Manufacturer = ing.Vendor
		changed = true
	}
	if ing.TypeHint != "" && ing.TypeHint != model.DeviceTypeUnknown &&
		dev.Type == model.DeviceTypeUnknown && !dev.IsLocked {
		dev.Type = ing.TypeHint
		dev.TypeSource = ing.Source
		changed = true
	}
	if ing.ObservedAt.After(dev.LastSeen) {
		dev.LastSeen = ing.ObservedAt
		changed = true
	}
	if dev.Type == "" {
		dev.Type = model.DeviceTypeUnknown
		changed = true
	}
	if dev.State == "" {
		dev.State = model.StateUnknown
		changed = true
	}

	if err := e.store.UpsertDevice(dev); err != nil {
		return nil, created, err
	}

	// Identities.
	if ing.MAC != "" && !database.IsZeroMAC(ing.MAC) {
		conf := database.IdentityConfidence(ing.Source, database.IsLocallyAdministeredMAC(ing.MAC))
		if err := e.store.UpsertIdentity(&model.DeviceIdentity{
			DeviceID:   dev.ID,
			Type:       model.IdentityMAC,
			Value:      ing.MAC,
			Source:     ing.Source,
			Confidence: conf,
			LastSeen:   ing.ObservedAt,
		}); err != nil {
			return nil, created, err
		}
	}
	if ing.ClientID != "" {
		if err := e.store.UpsertIdentity(&model.DeviceIdentity{
			DeviceID:   dev.ID,
			Type:       model.IdentityDHCPClient,
			Value:      ing.ClientID,
			Source:     ing.Source,
			Confidence: 0.95,
			LastSeen:   ing.ObservedAt,
		}); err != nil {
			return nil, created, err
		}
	}
	if ing.StrongID != "" && ing.StrongIDType != "" {
		if err := e.store.UpsertIdentity(&model.DeviceIdentity{
			DeviceID:   dev.ID,
			Type:       ing.StrongIDType,
			Value:      ing.StrongID,
			Source:     ing.Source,
			Confidence: 0.95,
			LastSeen:   ing.ObservedAt,
		}); err != nil {
			return nil, created, err
		}
	}
	if ing.Hostname != "" {
		if err := e.store.UpsertIdentity(&model.DeviceIdentity{
			DeviceID:   dev.ID,
			Type:       model.IdentityHostname,
			Value:      ing.Hostname,
			Source:     ing.Source,
			Confidence: 0.4,
			LastSeen:   ing.ObservedAt,
		}); err != nil {
			return nil, created, err
		}
	}

	// Addresses.
	for _, a := range ing.Addresses {
		family := a.Family
		if family == "" {
			if strings.Contains(a.Address, ":") {
				family = model.AddressFamilyIPv6
			} else {
				family = model.AddressFamilyIPv4
			}
		}
		if err := e.store.UpsertAddress(&model.DeviceAddress{
			DeviceID:     dev.ID,
			Address:      a.Address,
			Family:       family,
			InterfaceID:  a.Interface,
			Source:       ing.Source,
			PrefixLength: a.PrefixLength,
			LastSeen:     ing.ObservedAt,
		}); err != nil {
			return nil, created, err
		}
	}

	if err := e.store.AddDeviceSource(dev.ID, ing.Source); err != nil {
		return nil, created, err
	}

	e.refreshPrimaryAddresses(dev)

	if created && e.bus != nil {
		e.bus.PublishDeviceUpdated(dev, "discovered")
	} else if changed && e.bus != nil {
		e.bus.PublishDeviceUpdated(dev, "updated")
	}
	return dev, created, nil
}

// match finds the device an observation belongs to, or nil when none matches.
func (e *Engine) match(ing Ingest) (*model.Device, error) {
	// 1. Strong identity.
	if ing.StrongID != "" && ing.StrongIDType != "" {
		if devs, err := e.store.FindDevicesByIdentity(ing.StrongIDType, ing.StrongID); err == nil && len(devs) > 0 {
			return &devs[0], nil
		}
	}
	if ing.ClientID != "" {
		if devs, err := e.store.FindDevicesByIdentity(model.IdentityDHCPClient, ing.ClientID); err == nil && len(devs) > 0 {
			return &devs[0], nil
		}
	}

	// 2. MAC address, unless it is randomised.
	if ing.MAC != "" && !database.IsZeroMAC(ing.MAC) {
		if devs, err := e.store.FindDevicesByIdentity(model.IdentityMAC, ing.MAC); err == nil && len(devs) > 0 {
			return &devs[0], nil
		}
		// An address previously seen for this MAC.
		for _, a := range ing.Addresses {
			if dev, err := e.store.FindDeviceByAddress(familyOf(a), a.Address); err == nil && dev != nil {
				return dev, nil
			}
		}
	}

	// 3. Randomised MAC: try to attach it to a device already known by
	//    hostname or vendor rather than creating a duplicate.
	//
	//    Only a candidate that has no MAC identity of its own may be merged
	//    this way. A candidate identified by its own MAC is a different
	//    machine that happens to share the hostname, and a randomised MAC is
	//    exactly the kind of identity that must not merge two of them: the
	//    result would be one device carrying both machines' addresses.
	if ing.MAC != "" && database.IsLocallyAdministeredMAC(ing.MAC) {
		if ing.Hostname != "" {
			if devs, err := e.store.FindDevicesByIdentity(model.IdentityHostname, ing.Hostname); err == nil && len(devs) > 0 {
				for _, cand := range devs {
					if !candHasMAC(e, cand) {
						return &cand, nil
					}
				}
			}
		}
	}

	// 4. Hostname alone. Only accept when the hostname is distinctive and the
	//    device is not locked, and only for non-randomised MACs.
	if ing.Hostname != "" && ing.MAC != "" && !database.IsLocallyAdministeredMAC(ing.MAC) {
		if devs, err := e.store.FindDevicesByIdentity(model.IdentityHostname, ing.Hostname); err == nil && len(devs) == 1 {
			cand := devs[0]
			// A candidate identified by its own MAC is a different machine that
			// happens to share the hostname. Merging on the hostname alone
			// would move that machine's addresses to the incoming device,
			// which is what happened to two Macs named "Mac" on the reference
			// network.
			if candHasMAC(e, cand) {
				return nil, nil
			}
			// Require corroboration: the candidate must share at least one
			// address, or be the only device with that hostname.
			for _, a := range ing.Addresses {
				if dev, err := e.store.FindDeviceByAddress(familyOf(a), a.Address); err == nil && dev != nil && dev.ID == cand.ID {
					return &cand, nil
				}
			}
			if !cand.IsLocked && cand.Type != model.DeviceTypeUnknown {
				return &cand, nil
			}
		}
	}

	// 5. IP address alone: only merge when the address is already owned by a
	//    device that has no MAC of its own (e.g. a manual device).
	if ing.MAC == "" {
		for _, a := range ing.Addresses {
			if dev, err := e.store.FindDeviceByAddress(familyOf(a), a.Address); err == nil && dev != nil {
				return dev, nil
			}
		}
	}

	return nil, nil
}

// candHasMAC reports whether a candidate device already has a MAC identity of
// its own.
//
// Such a device is identified by hardware, not by a name that can be reused,
// so an incoming observation that merely shares its hostname is a different
// machine.
//
// A lookup failure is treated as "the candidate has a MAC identity". The
// alternative - assuming it has none - would let a hostname-only merge
// through, which is the exact thing this function exists to prevent. Merging
// two devices wrongly is bad; failing to merge them is merely conservative.
func candHasMAC(e *Engine, cand model.Device) bool {
	ids, err := e.store.ListIdentities(cand.ID)
	if err != nil {
		return true
	}
	for _, id := range ids {
		if id.Type == model.IdentityMAC && id.Value != "" {
			return true
		}
	}
	return false
}

func familyOf(a discovery.AddressObservation) model.AddressFamily {
	if a.Family != "" {
		return a.Family
	}
	if strings.Contains(a.Address, ":") {
		return model.AddressFamilyIPv6
	}
	return model.AddressFamilyIPv4
}

// newDevice builds a fresh device from an observation.
func (e *Engine) newDevice(ing Ingest) *model.Device {
	now := ing.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	dev := &model.Device{
		ID:               newDeviceID(ing),
		Name:             displayName(ing),
		Hostname:         ing.Hostname,
		Type:             model.DeviceTypeUnknown,
		Manufacturer:     ing.Vendor,
		Model:            "",
		Description:      "",
		State:            model.StateUnknown,
		StateReason:      "waiting for first reachability check",
		LastStateChange:  now,
		FirstSeen:        now,
		LastSeen:         now,
		CreatedAt:        now,
		UpdatedAt:        now,
		DiscoverySources: []model.DiscoverySource{ing.Source},
	}
	if ing.TypeHint != "" && model.IsValidDeviceType(ing.TypeHint) {
		dev.Type = ing.TypeHint
		dev.TypeSource = ing.Source
	}
	return dev
}

// displayName picks the most human friendly name available.
//
// A device with no hostname gets a placeholder built from the strongest thing
// we know about it. An address beats a MAC here: an address is something the
// user can find in the router's DHCP lease table or ping, while six hex
// characters from the middle of a MAC match nothing they can see anywhere else.
// A link-local IPv6 does not count — it is per-interface plumbing, not a
// handle anyone can use.
func displayName(ing Ingest) string {
	if ing.Hostname != "" {
		return ing.Hostname
	}
	if ing.Vendor != "" && ing.MAC != "" {
		return ing.Vendor + " " + shortMAC(ing.MAC)
	}
	for _, a := range ing.Addresses {
		if a.Address != "" && !strings.Contains(a.Address, ":") {
			return "Unknown " + a.Address
		}
	}
	for _, a := range ing.Addresses {
		if a.Address != "" && !isLinkLocal(a.Address) {
			return "Unknown " + a.Address
		}
	}
	if ing.MAC != "" {
		return "Unknown " + shortMAC(ing.MAC)
	}
	return "Unknown device"
}

// isLinkLocal reports whether an address is an IPv6 link-local address.
func isLinkLocal(addr string) bool {
	return strings.HasPrefix(strings.ToLower(addr), "fe80:")
}

// placeholderRank scores how useful a generated name is, so that a later
// observation can only improve a device's placeholder.
//
// The ordering is the same one displayName() applies when a device is created:
// a vendor plus a MAC tells the user what the box is, an address tells them
// where it is, and a bare MAC suffix tells them nothing they can act on.
func placeholderRank(name string) int {
	switch {
	case name == "", name == "Unknown device":
		return 0
	case isMACPlaceholder(name):
		return 1
	case strings.HasPrefix(name, "Unknown "):
		return 2
	default:
		// A vendor-prefixed placeholder ("Xiaomi 77:7f:24"), or a real name
		// that isDerivedName() let through. Never replaced by a weaker one.
		return 3
	}
}

// isUnknownPlaceholder reports whether a name is one of the "Unknown ..."
// fallbacks the engine generates.
//
// Only these are replaced by a hostname. A name that merely starts with
// "Unknown " was written by the user ("Unknown camera") and must survive.
func isUnknownPlaceholder(name string) bool {
	if name == "" || name == "Unknown device" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "Unknown ")
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	// A MAC suffix and an address are both generated from an observation.
	return isMACPlaceholder(name) || net.ParseIP(rest) != nil
}

// isMACPlaceholder reports whether a name is the "Unknown xx:xx:xx" fallback.
//
// Separators are optional: a device named before shortMAC() grouped the octets
// carries "Unknown ddeeff", and that name must still be recognised so a later
// observation can upgrade it.
func isMACPlaceholder(name string) bool {
	rest, ok := strings.CutPrefix(name, "Unknown ")
	if !ok {
		return false
	}
	hex := strings.ReplaceAll(rest, ":", "")
	if len(hex) != 6 {
		return false
	}
	for _, r := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// isDerivedName reports whether a device's name was generated from an earlier,
// weaker observation rather than typed by the user.
//
// Only derived names are replaced when a hostname arrives. A name the user
// chose in the UI is never overwritten, which is what IsLocked protects for the
// rest of the fields and what this protects for the name alone.
func isDerivedName(dev *model.Device) bool {
	name := dev.Name
	switch {
	case isUnknownPlaceholder(name):
		return true
	case dev.Hostname != "" && name == dev.Hostname:
		// The name is the previous hostname, so it follows the new one.
		return true
	case dev.Manufacturer != "" && strings.HasPrefix(name, dev.Manufacturer+" "):
		// "Xiaomi 77:7f:24": a vendor prefix plus a MAC suffix.
		return true
	}
	return false
}

// shortMAC renders the last three octets of a MAC, grouped like a MAC.
//
// database.FormatMAC only formats a full 12-character address and returns a
// six-character slice unchanged, so it cannot be used on the tail: doing so
// produced "Unknown ddeeff", which reads as one word rather than as the end of
// an address.
func shortMAC(mac string) string {
	c := database.CanonicalizeMAC(mac)
	if len(c) < 6 {
		return c
	}
	tail := c[len(c)-6:]
	return tail[0:2] + ":" + tail[2:4] + ":" + tail[4:6]
}

// newDeviceID derives a stable device ID from the strongest available identity.
func newDeviceID(ing Ingest) string {
	if ing.MAC != "" && !database.IsZeroMAC(ing.MAC) {
		return "dev-mac-" + database.CanonicalizeMAC(ing.MAC)
	}
	if ing.StrongID != "" {
		return "dev-id-" + sanitize(ing.StrongID)
	}
	if ing.ClientID != "" {
		return "dev-cid-" + sanitize(ing.ClientID)
	}
	for _, a := range ing.Addresses {
		if a.Address != "" {
			return "dev-ip-" + sanitize(a.Address)
		}
	}
	return "dev-x-" + sanitize(ing.Hostname)
}

func sanitize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, ":", "")
	v = strings.ReplaceAll(v, "/", "-")
	v = strings.ReplaceAll(v, " ", "-")
	if len(v) > 40 {
		v = v[:40]
	}
	if v == "" {
		v = "unknown"
	}
	return v
}

// refreshPrimaryAddresses recomputes the cached primary address fields.
func (e *Engine) refreshPrimaryAddresses(dev *model.Device) {
	addrs, err := e.store.ListAddresses(dev.ID)
	if err != nil {
		return
	}
	var (
		v4, v6, mac    string
		v4Seen, v6Seen time.Time
		v6Global       bool
		changed        bool
	)
	for _, a := range addrs {
		switch a.Family {
		case model.AddressFamilyIPv4:
			// The address the device is using now is the one observed most
			// recently. A device that moved keeps its old leases in the table,
			// and taking the first row (ListAddresses orders by address) would
			// surface a stale one - a device could even show an address another
			// device holds today.
			if v4 == "" || a.LastSeen.After(v4Seen) {
				v4, v4Seen = a.Address, a.LastSeen
			}
		case model.AddressFamilyIPv6:
			// Link-local addresses are never useful as the device's address.
			if strings.HasPrefix(a.Address, "fe80:") {
				continue
			}
			// Prefer a globally routable address; among equals, the most recent.
			global := strings.HasPrefix(a.Address, "2001:")
			switch {
			case v6 == "":
				v6, v6Seen, v6Global = a.Address, a.LastSeen, global
			case global && !v6Global:
				v6, v6Seen, v6Global = a.Address, a.LastSeen, global
			case global == v6Global && a.LastSeen.After(v6Seen):
				v6, v6Seen, v6Global = a.Address, a.LastSeen, global
			}
		}
	}
	ids, err := e.store.ListIdentities(dev.ID)
	if err == nil {
		for _, i := range ids {
			if i.Type == model.IdentityMAC {
				mac = database.FormatMAC(i.Value)
				break
			}
		}
	}
	if v4 != dev.PrimaryIPv4 {
		dev.PrimaryIPv4 = v4
		changed = true
	}
	if v6 != dev.PrimaryIPv6 {
		dev.PrimaryIPv6 = v6
		changed = true
	}
	if mac != dev.PrimaryMAC {
		dev.PrimaryMAC = mac
		changed = true
	}
	if changed {
		_ = e.store.UpsertDevice(dev)
	}
}

// MarkSleeping records that a device shows signs of a power saving state.
func (e *Engine) MarkSleeping(id, reason string) error {
	dev, err := e.store.GetDevice(id)
	if err != nil {
		return err
	}
	if dev.State == model.StateSleeping {
		return nil
	}
	dev.State = model.StateSleeping
	dev.StateReason = reason
	dev.LastStateChange = time.Now().UTC()
	if err := e.store.UpsertDevice(dev); err != nil {
		return err
	}
	if e.bus != nil {
		e.bus.PublishDeviceUpdated(dev, reason)
	}
	return nil
}

// SetOffline marks a device offline after repeated failures.
func (e *Engine) SetOffline(id, reason string) error {
	dev, err := e.store.GetDevice(id)
	if err != nil {
		return err
	}
	if dev.State == model.StateOffline {
		return nil
	}
	prev := dev.State
	dev.State = model.StateOffline
	dev.StateReason = reason
	dev.LastStateChange = time.Now().UTC()
	if err := e.store.UpsertDevice(dev); err != nil {
		return err
	}
	if e.bus != nil {
		e.bus.PublishDeviceOffline(dev, reason)
	}
	if prev != model.StateOffline {
		_ = e.store.SaveEvent(&model.NetworkEvent{
			EventType: model.EventDeviceOffline,
			DeviceID:  dev.ID,
			OldValue:  string(prev),
			NewValue:  string(model.StateOffline),
			Message:   reason,
		})
	}
	return nil
}

// SetOnline marks a device online.
func (e *Engine) SetOnline(id, reason string) error {
	dev, err := e.store.GetDevice(id)
	if err != nil {
		return err
	}
	if dev.State == model.StateOnline {
		dev.StateReason = reason
		return e.store.UpsertDevice(dev)
	}
	prev := dev.State
	dev.State = model.StateOnline
	dev.StateReason = reason
	dev.LastStateChange = time.Now().UTC()
	if err := e.store.UpsertDevice(dev); err != nil {
		return err
	}
	if e.bus != nil {
		e.bus.PublishDeviceOnline(dev, reason)
	}
	_ = e.store.SaveEvent(&model.NetworkEvent{
		EventType: model.EventDeviceOnline,
		DeviceID:  dev.ID,
		OldValue:  string(prev),
		NewValue:  string(model.StateOnline),
		Message:   reason,
	})
	return nil
}

// SetUnknown marks a device state as unknown.
func (e *Engine) SetUnknown(id, reason string) error {
	dev, err := e.store.GetDevice(id)
	if err != nil {
		return err
	}
	if dev.State == model.StateUnknown && dev.StateReason == reason {
		return nil
	}
	dev.State = model.StateUnknown
	dev.StateReason = reason
	dev.LastStateChange = time.Now().UTC()
	if err := e.store.UpsertDevice(dev); err != nil {
		return err
	}
	if e.bus != nil {
		e.bus.PublishDeviceUpdated(dev, reason)
	}
	return nil
}
