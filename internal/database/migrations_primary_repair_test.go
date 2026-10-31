package database

import (
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// A device must never advertise a primary address that no longer exists.
//
// Migration 0005 removed the ghost addresses, which left the cached primary
// pointing at a removed row; migration 0006 clears that cache.
func TestRepairPrimaryAddressesClearsStaleCache(t *testing.T) {
	s := newTestStore(t)

	dev := &model.Device{ID: "dev-stale", Name: "Mac", PrimaryIPv4: "192.168.1.10"}
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatal(err)
	}
	live := &model.Device{ID: "dev-live", Name: "iPhone", PrimaryIPv4: "192.168.100.151"}
	if err := s.UpsertDevice(live); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAddress(&model.DeviceAddress{
		DeviceID: live.ID,
		Address:  "192.168.100.151",
		Family:   model.AddressFamilyIPv4,
		Source:   model.SourceDHCP,
	}); err != nil {
		t.Fatal(err)
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/0006_repair_primary_addresses.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := s.DB().Exec(string(sqlBytes)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	stale, err := s.GetDevice(dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.PrimaryIPv4 != "" {
		t.Fatalf("a stale primary address survived: %q", stale.PrimaryIPv4)
	}

	ok, err := s.GetDevice(live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok.PrimaryIPv4 != "192.168.100.151" {
		t.Fatalf("a valid primary address was cleared: %q", ok.PrimaryIPv4)
	}
}
