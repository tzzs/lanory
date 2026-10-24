package database

import (
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// seedDevices inserts devices in the given order so that the test would fail
// if ListDevices accidentally relied on insertion order.
func seedDevices(t *testing.T, s *Store, devices []*model.Device) {
	t.Helper()
	for _, d := range devices {
		if err := s.UpsertDevice(d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}
}

func deviceIDs(devices []model.Device) []string {
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = d.ID
	}
	return out
}

func assertOrder(t *testing.T, got []model.Device, want []string) {
	t.Helper()
	ids := deviceIDs(got)
	if len(ids) != len(want) {
		t.Fatalf("got %d devices, want %d: %v", len(ids), len(want), ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, ids, want)
		}
	}
}

func TestListDevicesOnlineFirst(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDevices(t, s, []*model.Device{
		{ID: "dev-offline-new", Name: "offline-new", State: model.StateOffline, LastSeen: base.Add(3 * time.Hour)},
		{ID: "dev-online-old", Name: "online-old", State: model.StateOnline, LastSeen: base.Add(1 * time.Hour)},
		{ID: "dev-offline-old", Name: "offline-old", State: model.StateOffline, LastSeen: base},
		{ID: "dev-online-mid", Name: "online-mid", State: model.StateOnline, LastSeen: base.Add(2 * time.Hour)},
	})

	devices, total, err := s.ListDevices(DeviceFilter{
		OnlineFirst: true,
		Sort:        "last_seen",
		Order:       "desc",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	// Online devices lead even though an offline device was seen more recently.
	assertOrder(t, devices, []string{
		"dev-online-mid", "dev-online-old", "dev-offline-new", "dev-offline-old",
	})
}

func TestListDevicesOnlineFirstReversesWithinGroupsOnly(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDevices(t, s, []*model.Device{
		{ID: "dev-online-old", Name: "online-old", State: model.StateOnline, LastSeen: base.Add(1 * time.Hour)},
		{ID: "dev-offline-new", Name: "offline-new", State: model.StateOffline, LastSeen: base.Add(3 * time.Hour)},
		{ID: "dev-online-mid", Name: "online-mid", State: model.StateOnline, LastSeen: base.Add(2 * time.Hour)},
		{ID: "dev-offline-old", Name: "offline-old", State: model.StateOffline, LastSeen: base},
	})

	devices, _, err := s.ListDevices(DeviceFilter{
		OnlineFirst: true,
		Sort:        "last_seen",
		Order:       "asc",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Ascending reverses each group, but never lifts offline above online.
	assertOrder(t, devices, []string{
		"dev-online-old", "dev-online-mid", "dev-offline-old", "dev-offline-new",
	})
}

func TestListDevicesWithoutOnlineFirstKeepsPureOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDevices(t, s, []*model.Device{
		{ID: "dev-offline-new", Name: "offline-new", State: model.StateOffline, LastSeen: base.Add(3 * time.Hour)},
		{ID: "dev-online-old", Name: "online-old", State: model.StateOnline, LastSeen: base.Add(1 * time.Hour)},
		{ID: "dev-online-mid", Name: "online-mid", State: model.StateOnline, LastSeen: base.Add(2 * time.Hour)},
		{ID: "dev-offline-old", Name: "offline-old", State: model.StateOffline, LastSeen: base},
	})

	devices, _, err := s.ListDevices(DeviceFilter{Sort: "last_seen", Order: "desc"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Internal callers (topology builder, monitoring) must keep the raw order.
	assertOrder(t, devices, []string{
		"dev-offline-new", "dev-online-mid", "dev-online-old", "dev-offline-old",
	})
}

func TestListDevicesOnlineFirstWithNameSort(t *testing.T) {
	s := newTestStore(t)
	seedDevices(t, s, []*model.Device{
		{ID: "dev-zeta", Name: "zeta", State: model.StateOffline},
		{ID: "dev-beta", Name: "beta", State: model.StateOnline},
		{ID: "dev-alpha", Name: "alpha", State: model.StateOffline},
		{ID: "dev-gamma", Name: "gamma", State: model.StateOnline},
	})

	devices, _, err := s.ListDevices(DeviceFilter{
		OnlineFirst: true,
		Sort:        "name",
		Order:       "asc",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertOrder(t, devices, []string{"dev-beta", "dev-gamma", "dev-alpha", "dev-zeta"})
}
