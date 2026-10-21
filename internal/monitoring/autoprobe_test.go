package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// TestEnsureProbesCreatesOnePerDevice is the regression guard for the bug that
// left every discovered device in the "unknown" state forever: discovery wrote
// devices, the probe engine only ran probes that already existed, and nothing
// connected the two.
func TestEnsureProbesCreatesOnePerDevice(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-a", "Alpha", "192.0.2.10", model.DeviceTypeDesktop)
	seedDevice(t, store, "dev-b", "Beta", "192.0.2.11", model.DeviceTypeSmartphone)

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	created, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig())
	if err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	if created != 2 {
		t.Fatalf("created %d probes, want 2", created)
	}

	for _, id := range []string{"dev-a", "dev-b"} {
		probes, err := store.ListProbesByDevice(id)
		if err != nil {
			t.Fatalf("list probes for %s: %v", id, err)
		}
		if len(probes) != 1 {
			t.Fatalf("%s has %d probes, want 1", id, len(probes))
		}
		if probes[0].Protocol != model.ProbeICMP {
			t.Errorf("%s probe protocol = %q, want icmp", id, probes[0].Protocol)
		}
		if probes[0].State != model.StateUnknown {
			t.Errorf("%s probe starts in %q, want unknown", id, probes[0].State)
		}
		if probes[0].FailureThreshold < 1 {
			t.Errorf("%s probe failure threshold = %d", id, probes[0].FailureThreshold)
		}
	}
}

// TestEnsureProbesIsIdempotent: repeated discovery runs must not duplicate
// probes or reset the counters the state machine depends on.
func TestEnsureProbesIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-a", "Alpha", "192.0.2.10", model.DeviceTypeDesktop)
	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())

	for i := 0; i < 5; i++ {
		if _, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig()); err != nil {
			t.Fatalf("EnsureProbes run %d: %v", i, err)
		}
	}
	probes, err := store.ListProbesByDevice("dev-a")
	if err != nil {
		t.Fatalf("list probes: %v", err)
	}
	if len(probes) != 1 {
		t.Errorf("after 5 runs there are %d probes, want 1", len(probes))
	}
}

// TestEnsureProbesSkipsDevicesWithoutAddress covers manual nodes such as a fiber
// run or an optical modem: there is nothing to ping.
func TestEnsureProbesSkipsDevicesWithoutAddress(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-fiber", "Fiber", "", model.DeviceTypeNetworkFiber)
	seedDevice(t, store, "dev-noip", "No IP", "", model.DeviceTypeUnknown)

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	created, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig())
	if err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	if created != 0 {
		t.Errorf("created %d probes for address-less devices, want 0", created)
	}
}

// TestEnsureProbesRespectsSkipTypes: the Internet node and generic placeholders
// are not hosts.
func TestEnsureProbesRespectsSkipTypes(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-net", "Internet", "192.0.2.1", model.DeviceTypeInternet)
	seedDevice(t, store, "dev-pc", "PC", "192.0.2.20", model.DeviceTypeDesktop)

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	created, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig())
	if err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	if created != 1 {
		t.Errorf("created %d probes, want only the desktop", created)
	}
}

// TestEnsureProbesHonoursDisabled: the feature can be switched off.
func TestEnsureProbesHonoursDisabled(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-a", "Alpha", "192.0.2.10", model.DeviceTypeDesktop)
	cfg := DefaultAutoProbeConfig()
	cfg.Enabled = false

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	created, err := e.EnsureProbes(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	if created != 0 {
		t.Errorf("created %d probes while disabled", created)
	}
}

// TestEnsureProbesLeavesExistingProbesAlone: a user-created probe with custom
// settings must not be overwritten.
func TestEnsureProbesLeavesExistingProbesAlone(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-a", "Alpha", "192.0.2.10", model.DeviceTypeDesktop)
	custom := &model.ProbeTarget{
		Name: "custom", Kind: model.ProbeKindDevice, Target: "192.0.2.10",
		Protocol: model.ProbeTCP, Port: 22, DeviceID: "dev-a",
		Interval: 300, Timeout: 5, Enabled: true,
		FailureThreshold: 7, RecoverConfirm: 2, State: model.StateUnknown,
	}
	if err := store.CreateProbe(custom); err != nil {
		t.Fatalf("seed probe: %v", err)
	}

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	if _, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig()); err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	probes, _ := store.ListProbesByDevice("dev-a")
	if len(probes) != 1 {
		t.Fatalf("%d probes, want the user's single one", len(probes))
	}
	if probes[0].Protocol != model.ProbeTCP || probes[0].Port != 22 {
		t.Errorf("user probe was modified: protocol=%q port=%d", probes[0].Protocol, probes[0].Port)
	}
}

// TestEnsureProbesUsesIPv6WhenThereIsNoIPv4 covers an IPv6-only host.
func TestEnsureProbesUsesIPv6WhenThereIsNoIPv4(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-v6", "v6host", "", model.DeviceTypeDesktop)
	// Give it an IPv6 address only.
	if err := store.UpsertAddress(&model.DeviceAddress{
		DeviceID: "dev-v6", Address: "2001:db8::10",
		Family: model.AddressFamilyIPv6, Source: model.SourceNDP,
	}); err != nil {
		t.Fatalf("seed address: %v", err)
	}
	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	if _, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig()); err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	probes, _ := store.ListProbesByDevice("dev-v6")
	if len(probes) != 1 {
		t.Fatalf("%d probes, want 1", len(probes))
	}
	if probes[0].Target != "2001:db8::10" {
		t.Errorf("probe target = %q, want the IPv6 address", probes[0].Target)
	}
}

// --- helpers -------------------------------------------------------------

func openTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(t.TempDir() + "/mon.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

func seedDevice(t *testing.T, store *database.Store, id, name, ipv4 string, typ model.DeviceType) {
	t.Helper()
	now := time.Now().UTC()
	dev := &model.Device{
		ID: id, Name: name, Hostname: name, Type: typ,
		State: model.StateUnknown, FirstSeen: now, LastSeen: now,
		PrimaryIPv4: ipv4,
	}
	if err := store.UpsertDevice(dev); err != nil {
		t.Fatalf("seed device %s: %v", id, err)
	}
	if ipv4 != "" {
		if err := store.UpsertAddress(&model.DeviceAddress{
			DeviceID: id, Address: ipv4, Family: model.AddressFamilyIPv4,
			Source: model.SourceDHCP, IsPrimary: true,
		}); err != nil {
			t.Fatalf("seed address: %v", err)
		}
	}
}

// TestEnsureProbesPrefersIPv4 locks the address choice when a device has both.
//
// A device discovered over NDP first and ARP later ends up with an IPv6 and an
// IPv4 address. The router often has no route to the device's global IPv6
// prefix while the IPv4 address is reachable, so probing the IPv6 address
// reports a reachable host as offline. The probe must target the IPv4 address.
func TestEnsureProbesPrefersIPv4(t *testing.T) {
	store := openTestStore(t)
	seedDevice(t, store, "dev-both", "dual", "192.0.2.40", model.DeviceTypeDesktop)
	if err := store.UpsertAddress(&model.DeviceAddress{
		DeviceID: "dev-both", Address: "2001:db8::40",
		Family: model.AddressFamilyIPv6, Source: model.SourceNDP,
	}); err != nil {
		t.Fatalf("seed v6: %v", err)
	}

	e := NewEngine(store, nil, nil, nil, DefaultEngineConfig())
	if _, err := e.EnsureProbes(context.Background(), DefaultAutoProbeConfig()); err != nil {
		t.Fatalf("EnsureProbes: %v", err)
	}
	probes, _ := store.ListProbesByDevice("dev-both")
	if len(probes) != 1 {
		t.Fatalf("%d probes, want 1", len(probes))
	}
	if probes[0].Target != "192.0.2.40" {
		t.Errorf("probe target = %q, want the IPv4 address 192.0.2.40", probes[0].Target)
	}
}
