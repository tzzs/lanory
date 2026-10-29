package device

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// noopPublisher satisfies the engine's publisher without recording anything.
type noopPublisher struct{}

func (noopPublisher) PublishDeviceUpdated(*model.Device, string) {}
func (noopPublisher) PublishDeviceOnline(*model.Device, string)  {}
func (noopPublisher) PublishDeviceOffline(*model.Device, string) {}
func (noopPublisher) PublishTopologyUpdated(string)              {}

func newIdentityEngine(t *testing.T) (*Engine, *database.Store) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)
	return New(store, noopPublisher{}), store
}

func arpIngest(mac, address, hostname string) Ingest {
	return Ingest{
		MAC:        mac,
		Hostname:   hostname,
		Addresses:  []discovery.AddressObservation{{Address: address}},
		Source:     model.SourceARP,
		ObservedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
	}
}

// A zero MAC is the kernel's unresolved sentinel. It must never become an
// identity or a device name such as "Unknown 000000".
func TestResolveIgnoresZeroMAC(t *testing.T) {
	eng, _ := newIdentityEngine(t)

	dev, _, err := eng.Resolve(arpIngest("00:00:00:00:00:00", "192.168.1.30", ""))
	if err != nil {
		t.Fatal(err)
	}
	if dev.ID != "dev-ip-192.168.1.30" {
		t.Fatalf("device id = %q, want the address-based id", dev.ID)
	}
	if dev.Name == "Unknown 000000" {
		t.Fatalf("zero MAC leaked into the name: %q", dev.Name)
	}
}

// A device whose hostname was recorded before the name could be refreshed must
// still get its name fixed: the refresh used to require the hostname to
// change, which never happens again for that device.
func TestResolveRefreshesPlaceholderWhenHostnameUnchanged(t *testing.T) {
	eng, store := newIdentityEngine(t)

	dev, _, err := eng.Resolve(arpIngest("aa:bb:cc:dd:ee:02", "192.168.1.32", ""))
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name != "Unknown 192.168.1.32" {
		t.Fatalf("expected a placeholder name, got %q", dev.Name)
	}
	// Record the hostname without changing the name.
	if _, err := store.UpdateDevice(dev.ID, map[string]any{"hostname": "printer"}); err != nil {
		t.Fatal(err)
	}

	got, _, err := eng.Resolve(arpIngest("aa:bb:cc:dd:ee:02", "192.168.1.32", "printer"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "printer" {
		t.Fatalf("the placeholder was not refreshed: %q", got.Name)
	}
}

// A vendor-prefixed name says more than the hostname, so it must survive.
func TestResolveKeepsVendorNameOverHostname(t *testing.T) {
	eng, store := newIdentityEngine(t)

	dev, _, err := eng.Resolve(Ingest{
		MAC:        "aa:bb:cc:dd:ee:03",
		Vendor:     "Xiaomi",
		Addresses:  []discovery.AddressObservation{{Address: "192.168.1.33"}},
		Source:     model.SourceARP,
		ObservedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name != "Xiaomi dd:ee:03" {
		t.Fatalf("unexpected name %q", dev.Name)
	}
	if _, err := store.UpdateDevice(dev.ID, map[string]any{"hostname": "router"}); err != nil {
		t.Fatal(err)
	}

	got, _, err := eng.Resolve(Ingest{
		MAC:        "aa:bb:cc:dd:ee:03",
		Vendor:     "Xiaomi",
		Hostname:   "router",
		Addresses:  []discovery.AddressObservation{{Address: "192.168.1.33"}},
		Source:     model.SourceARP,
		ObservedAt: time.Date(2026, 10, 30, 12, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Xiaomi dd:ee:03" {
		t.Fatalf("the vendor name was overwritten: %q", got.Name)
	}
}

// A device the user marked as manual keeps its name even when that name looks
// like a generated placeholder.
func TestResolveKeepsManualDeviceName(t *testing.T) {
	eng, store := newIdentityEngine(t)

	dev, _, err := eng.Resolve(arpIngest("aa:bb:cc:dd:ee:01", "192.168.1.31", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDevice(dev.ID, map[string]any{
		"is_manual": true,
		"name":      "Unknown 192.168.1.31",
	}); err != nil {
		t.Fatal(err)
	}

	got, _, err := eng.Resolve(arpIngest("aa:bb:cc:dd:ee:01", "192.168.1.31", "printer"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Unknown 192.168.1.31" {
		t.Fatalf("manual name was overwritten: %q", got.Name)
	}
}
