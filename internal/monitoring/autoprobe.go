package monitoring

import (
	"context"
	"fmt"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// DeviceLister is the slice of the store the auto-provisioner needs to see which
// devices exist and whether they already have a probe.
//
// Declared as an interface so the engine stays decoupled from the database
// package; *database.Store satisfies it as is.
type DeviceLister interface {
	ListDevices(f database.DeviceFilter) ([]model.Device, int, error)
	ListAddresses(deviceID string) ([]model.DeviceAddress, error)
	ListActiveAddresses(deviceID string, now time.Time) ([]model.DeviceAddress, error)
	ListProbesByDevice(deviceID string) ([]model.ProbeTarget, error)
	CreateProbe(p *model.ProbeTarget) error
}

// AutoProbeConfig controls automatic probe provisioning.
type AutoProbeConfig struct {
	// Enabled turns on provisioning for newly discovered devices.
	Enabled bool
	// Interval is the per-device probe interval.
	Interval time.Duration
	// Timeout is the per-probe timeout.
	Timeout time.Duration
	// FailureThreshold and RecoverConfirm are copied onto every probe.
	FailureThreshold int
	RecoverConfirm   int
	// SkipTypes are device types that are never probed automatically. Devices
	// with no address, or that live upstream of the WAN, are skipped regardless.
	SkipTypes []model.DeviceType
}

// DefaultAutoProbeConfig returns the configuration used by the service.
//
// Probing is on by default because a device that is discovered but never
// checked stays in the "unknown" state forever, which makes the whole monitoring
// page useless. The thresholds are deliberately forgiving: phones stop answering
// when they sleep, so one missed echo must not mark anything offline.
func DefaultAutoProbeConfig() AutoProbeConfig {
	return AutoProbeConfig{
		Enabled:          true,
		Interval:         60 * time.Second,
		Timeout:          3 * time.Second,
		FailureThreshold: 3,
		RecoverConfirm:   1,
		SkipTypes: []model.DeviceType{
			model.DeviceTypeInternet,     // synthetic node, has no address
			model.DeviceTypeNetworkFiber, // cabling, not a host
			model.DeviceTypeGenericNode,
		},
	}
}

// EnsureProbes creates a probe for every device that has an address and none yet.
//
// It is idempotent: a device that already has a probe is left alone, so calling
// it on every discovery run does not duplicate anything and does not reset
// counters that the state machine depends on.
//
// Returns the number of probes created.
func (e *Engine) EnsureProbes(ctx context.Context, cfg AutoProbeConfig) (int, error) {
	if !cfg.Enabled {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	lister, ok := e.store.(DeviceLister)
	if !ok || lister == nil {
		// The engine was constructed with a store that cannot enumerate devices
		// (the unit-test fake). Nothing to do.
		return 0, nil
	}

	devices, _, err := lister.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 1000})
	if err != nil {
		return 0, fmt.Errorf("monitoring: list devices for auto-provisioning: %w", err)
	}

	skip := make(map[model.DeviceType]bool, len(cfg.SkipTypes))
	for _, t := range cfg.SkipTypes {
		skip[t] = true
	}

	created := 0
	for i := range devices {
		dev := devices[i]
		if err := ctx.Err(); err != nil {
			return created, err
		}
		if skip[dev.Type] {
			continue
		}
		// A probe needs something to talk to. The cached primary_* columns on the
		// device row are only refreshed by the identity engine, so an address
		// written by another path (a manual device, or a v6-only host) may not be
		// there yet. Read the address table, which is authoritative, and fall
		// back to the cached columns.
		target := pickProbeAddress(dev, lister)
		if target == "" {
			continue
		}
		// A device with a probe already belongs to the user or a previous run.
		existing, err := lister.ListProbesByDevice(dev.ID)
		if err != nil {
			return created, fmt.Errorf("monitoring: list probes for %s: %w", dev.ID, err)
		}
		if len(existing) > 0 {
			continue
		}

		probe := &model.ProbeTarget{
			Name:             probeNameFor(dev),
			Kind:             model.ProbeKindDevice,
			Target:           target,
			Protocol:         model.ProbeICMP,
			DeviceID:         dev.ID,
			Interval:         int(cfg.Interval.Seconds()),
			Timeout:          int(cfg.Timeout.Seconds()),
			Enabled:          true,
			FailureThreshold: positiveOr(cfg.FailureThreshold, 3),
			RecoverConfirm:   positiveOr(cfg.RecoverConfirm, 1),
			State:            model.StateUnknown,
			StateReason:      "awaiting first probe",
			CreatedAt:        time.Now().UTC(),
		}
		if err := lister.CreateProbe(probe); err != nil {
			return created, fmt.Errorf("monitoring: create probe for %s: %w", dev.ID, err)
		}
		created++
	}
	return created, nil
}

// pickProbeAddress chooses the address to probe for a device.
//
// IPv4 first because it needs no scope handling, then a global IPv6 address, and
// link-local IPv6 last (it is only reachable with a zone, which a probe target
// string cannot carry reliably).
//
// Only active addresses are considered. A probe aimed at an address that was
// last seen months ago reports a live device as offline, which is worse than
// not probing it at all.
func pickProbeAddress(dev model.Device, lister DeviceLister) string {
	addrs, err := lister.ListActiveAddresses(dev.ID, time.Now().UTC())
	if err == nil {
		var v6, linkLocal string
		for _, a := range addrs {
			switch a.Family {
			case model.AddressFamilyIPv4:
				if a.Address != "" {
					return a.Address
				}
			case model.AddressFamilyIPv6:
				if a.Address == "" {
					continue
				}
				if isLinkLocalV6(a.Address) {
					if linkLocal == "" {
						linkLocal = a.Address
					}
					continue
				}
				if v6 == "" {
					v6 = a.Address
				}
			}
		}
		if v6 != "" {
			return v6
		}
		if linkLocal != "" {
			return linkLocal
		}
	}
	if dev.PrimaryIPv4 != "" {
		return dev.PrimaryIPv4
	}
	return dev.PrimaryIPv6
}

// isLinkLocalV6 reports whether an IPv6 address is in fe80::/10.
func isLinkLocalV6(addr string) bool {
	return len(addr) >= 4 && (addr[:4] == "fe80" || addr[:4] == "FE80")
}

// probeNameFor builds a stable, human readable probe name from a device.
func probeNameFor(dev model.Device) string {
	name := dev.Name
	if name == "" {
		name = dev.Hostname
	}
	if name == "" {
		name = dev.ID
	}
	if len(name) > 48 {
		name = name[:48]
	}
	return name
}
