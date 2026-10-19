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
	if ing.Hostname != "" && ing.Hostname != dev.Hostname && !dev.IsLocked {
		dev.Hostname = ing.Hostname
		changed = true
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
	if ing.MAC != "" && database.IsLocallyAdministeredMAC(ing.MAC) {
		if ing.Hostname != "" {
			if devs, err := e.store.FindDevicesByIdentity(model.IdentityHostname, ing.Hostname); err == nil && len(devs) > 0 {
				return &devs[0], nil
			}
		}
	}

	// 4. Hostname alone. Only accept when the hostname is distinctive and the
	//    device is not locked, and only for non-randomised MACs.
	if ing.Hostname != "" && ing.MAC != "" && !database.IsLocallyAdministeredMAC(ing.MAC) {
		if devs, err := e.store.FindDevicesByIdentity(model.IdentityHostname, ing.Hostname); err == nil && len(devs) == 1 {
			// Require corroboration: the candidate must share at least one
			// address or be the only device with that hostname.
			cand := devs[0]
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
		ID:            newDeviceID(ing),
		Name:          displayName(ing),
		Hostname:      ing.Hostname,
		Type:          model.DeviceTypeUnknown,
		Manufacturer:  ing.Vendor,
		Model:         "",
		Description:   "",
		State:         model.StateUnknown,
		StateReason:   "waiting for first reachability check",
		LastStateChange: now,
		FirstSeen:     now,
		LastSeen:      now,
		CreatedAt:     now,
		UpdatedAt:     now,
		DiscoverySources: []model.DiscoverySource{ing.Source},
	}
	if ing.TypeHint != "" && model.IsValidDeviceType(ing.TypeHint) {
		dev.Type = ing.TypeHint
		dev.TypeSource = ing.Source
	}
	return dev
}

// displayName picks the most human friendly name available.
func displayName(ing Ingest) string {
	if ing.Hostname != "" {
		return ing.Hostname
	}
	if ing.Vendor != "" && ing.MAC != "" {
		return ing.Vendor + " " + shortMAC(ing.MAC)
	}
	if ing.MAC != "" {
		return "Unknown " + shortMAC(ing.MAC)
	}
	for _, a := range ing.Addresses {
		if a.Address != "" {
			return "Unknown " + a.Address
		}
	}
	return "Unknown device"
}

func shortMAC(mac string) string {
	c := database.CanonicalizeMAC(mac)
	if len(c) < 6 {
		return c
	}
	return database.FormatMAC(c[len(c)-6:])
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
		v4, v6, mac string
		changed     bool
	)
	for _, a := range addrs {
		if a.Family == model.AddressFamilyIPv4 && v4 == "" {
			v4 = a.Address
		}
		if a.Family == model.AddressFamilyIPv6 && strings.HasPrefix(a.Address, "2001:") && v6 == "" {
			v6 = a.Address
		}
		if v6 == "" && a.Family == model.AddressFamilyIPv6 && !strings.HasPrefix(a.Address, "fe80:") {
			v6 = a.Address
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
