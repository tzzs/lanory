// Package resourcetest contains the resource and scale measurements Lanory's
// documentation promises.
//
// These are ordinary Go benchmarks and tests rather than a separate harness, so
// they run with the rest of the suite and cannot silently rot. Nothing here
// asserts a fixed memory figure: the point is to make the numbers measurable on
// the machine that will actually run Lanory, instead of advertising a figure
// that was true on a developer laptop.
package resourcetest

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/device"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
	"github.com/lanory/lanory/internal/topology"
)

// openStore returns a store on a temp database.
func openStore(t testing.TB) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

// seedDevices inserts n synthetic devices with addresses and identities, as the
// identity engine would.
func seedDevices(t testing.TB, store *database.Store, n int) []*model.Device {
	t.Helper()
	engine := device.New(store, nil)
	out := make([]*model.Device, 0, n)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		mac := fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", (i>>24)&0xff, (i>>16)&0xff, (i>>8)&0xff, i&0xff)
		ip := fmt.Sprintf("192.0.2.%d", (i%254)+1)
		dev, _, err := engine.Resolve(device.Ingest{
			MAC:      mac,
			Hostname: fmt.Sprintf("host-%d", i),
			Addresses: []discovery.AddressObservation{{
				Address: ip,
				Family:  model.AddressFamilyIPv4,
			}},
			Source:     model.SourceBridgeFDB,
			ObservedAt: now,
		})
		if err != nil {
			t.Fatalf("resolve device %d: %v", i, err)
		}
		out = append(out, dev)
	}
	return out
}

// BenchmarkDeviceUpsert measures the write path that runs on every discovery
// tick. This is the dominant cost on flash storage.
func BenchmarkDeviceUpsert(b *testing.B) {
	store := openStore(b)
	now := time.Now().UTC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := &model.Device{
			ID:        fmt.Sprintf("dev-bench-%d", i),
			Name:      "bench",
			Hostname:  "bench",
			Type:      model.DeviceTypeDesktop,
			State:     model.StateUnknown,
			FirstSeen: now,
			LastSeen:  now,
		}
		if err := store.UpsertDevice(d); err != nil {
			b.Fatalf("upsert: %v", err)
		}
	}
}

// BenchmarkBuildTopology measures the graph build the UI calls on every poll.
func BenchmarkBuildTopology(b *testing.B) {
	store := openStore(b)
	seedDevices(b, store, 60)
	svc := topology.NewService(store, config.Default())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Build(); err != nil {
			b.Fatalf("build: %v", err)
		}
	}
}

// BenchmarkOverview measures the aggregate query behind the overview page.
func BenchmarkOverview(b *testing.B) {
	store := openStore(b)
	seedDevices(b, store, 60)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.DeviceCounts(); err != nil {
			b.Fatalf("counts: %v", err)
		}
		if _, _, err := store.ListDevices(database.DeviceFilter{Limit: 50}); err != nil {
			b.Fatalf("list: %v", err)
		}
	}
}

// BenchmarkEventInsert measures history writes.
func BenchmarkEventInsert(b *testing.B) {
	store := openStore(b)
	// The foreign key is enforced, so the referenced device must exist.
	if err := store.UpsertDevice(&model.Device{
		ID: "dev-1", Name: "device", Type: model.DeviceTypeDesktop,
		State: model.StateUnknown,
	}); err != nil {
		b.Fatalf("seed device: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.SaveEvent(&model.NetworkEvent{
			EventType: model.EventDeviceOnline,
			DeviceID:  "dev-1",
			Message:   "bench",
		}); err != nil {
			b.Fatalf("save event: %v", err)
		}
	}
}

// BenchmarkSSEFanout measures publishing with several subscribers attached.
func BenchmarkSSEFanout(b *testing.B) {
	bus := events.New(256)
	const subs = 4
	for i := 0; i < subs; i++ {
		ch, cancel := bus.Subscribe()
		defer cancel()
		go func() {
			for range ch {
			}
		}()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.PublishTopologyUpdated("bench")
	}
}

// TestDatabaseGrowthStaysBounded checks the retention logic actually removes old
// rows, which is what keeps the database from growing forever on a router.
func TestDatabaseGrowthStaysBounded(t *testing.T) {
	store := openStore(t)
	// Events may reference a device, so one has to exist: the foreign key is
	// enforced and a dangling reference is rejected rather than silently stored.
	if err := store.UpsertDevice(&model.Device{
		ID: "dev-1", Name: "device", Type: model.DeviceTypeDesktop,
		State: model.StateUnknown,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	old := time.Now().UTC().Add(-90 * 24 * time.Hour)
	recent := time.Now().UTC()

	for i := 0; i < 200; i++ {
		if err := store.SaveEvent(&model.NetworkEvent{
			EventType: model.EventDeviceOnline,
			DeviceID:  "dev-1",
			Message:   "old",
			Timestamp: old,
		}); err != nil {
			t.Fatalf("save old event: %v", err)
		}
	}
	for i := 0; i < 50; i++ {
		if err := store.SaveEvent(&model.NetworkEvent{
			EventType: model.EventDeviceOnline,
			DeviceID:  "dev-1",
			Message:   "recent",
			Timestamp: recent,
		}); err != nil {
			t.Fatalf("save recent event: %v", err)
		}
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	removed, err := store.DeleteOldEvents(cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 200 {
		t.Errorf("pruned %d old events, want 200", removed)
	}
	_, total, err := store.ListEvents(database.EventFilter{Limit: 500})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 50 {
		t.Errorf("retained %d events, want 50", total)
	}
}

// TestRepeatedDiscoveryDoesNotGrowDatabase is the regression guard for the most
// dangerous scaling bug: a discovery loop that inserts a new row every tick.
func TestRepeatedDiscoveryDoesNotGrowDatabase(t *testing.T) {
	store := openStore(t)
	engine := device.New(store, nil)

	now := time.Now().UTC()
	for tick := 0; tick < 25; tick++ {
		// The same three devices, observed again and again.
		for _, mac := range []string{"02:00:00:00:00:01", "02:00:00:00:00:02", "02:00:00:00:00:03"} {
			_, _, err := engine.Resolve(device.Ingest{
				MAC:        mac,
				Hostname:   "host-" + mac[len(mac)-1:],
				Source:     model.SourceBridgeFDB,
				ObservedAt: now.Add(time.Duration(tick) * time.Minute),
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
		}
	}

	counts, err := store.DeviceCounts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Total != 3 {
		t.Errorf("after 25 discovery ticks with 3 stable devices there are %d devices, want 3", counts.Total)
	}
}

// TestDeviceScaleBuild measures graph construction at a realistic upper bound
// for a home network and records the cost rather than asserting a limit.
func TestDeviceScaleBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale test in -short mode")
	}
	const devices = 250
	store := openStore(t)
	seedDevices(t, store, devices)
	svc := topology.NewService(store, config.Default())

	start := time.Now()
	topo, err := svc.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("built topology for %d devices in %v (%d nodes, %d edges)",
		devices, elapsed.Round(time.Millisecond), len(topo.Nodes), len(topo.Edges))

	// A build that takes longer than a poll interval would make the UI stutter.
	if elapsed > 2*time.Second {
		t.Errorf("topology build for %d devices took %v, which exceeds the poll interval", devices, elapsed)
	}
}

// TestMemoryFootprint reports the heap after a realistic load. It does not
// assert an absolute number, because that depends on the platform; it fails only
// on a regression large enough to be a leak.
func TestMemoryFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory test in -short mode")
	}
	store := openStore(t)
	bus := events.New(512)
	seedDevices(t, store, 200)

	bus.Subscribe()
	svc := topology.NewService(store, config.Default())

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 100; i++ {
		if _, err := svc.Build(); err != nil {
			t.Fatalf("build: %v", err)
		}
		bus.PublishTopologyUpdated("mem")
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	grown := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("heap after 100 topology builds over 200 devices: %d KiB (delta %d KiB)",
		after.HeapAlloc/1024, grown/1024)

	// 100 builds must not retain the whole graph 100 times over.
	if grown > 64*1024*1024 {
		t.Errorf("heap grew by %d MiB over 100 builds, which suggests retained graphs", grown/(1024*1024))
	}
}

// TestConcurrentReadersDuringWrite exercises the read path while a discovery
// write is in flight, which is the situation during every poll tick.
func TestConcurrentReadersDuringWrite(t *testing.T) {
	store := openStore(t)
	engine := device.New(store, nil)
	svc := topology.NewService(store, config.Default())
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			_, _, _ = engine.Resolve(device.Ingest{
				MAC:        fmt.Sprintf("02:00:00:00:01:%02x", i),
				Hostname:   fmt.Sprintf("h%d", i),
				Source:     model.SourceBridgeFDB,
				ObservedAt: time.Now().UTC(),
			})
		}
	}()

	var errs int
	for i := 0; i < 40; i++ {
		if _, err := svc.Build(); err != nil {
			errs++
		}
		if _, _, err := store.ListDevices(database.DeviceFilter{Limit: 50}); err != nil {
			errs++
		}
	}
	<-done
	if errs > 0 {
		t.Errorf("%d read errors during concurrent writes", errs)
	}
	_ = ctx
}

// TestHistorySurvivesDeviceDeletion guards the nullable foreign key on
// network_events.
//
// History is most valuable exactly when the thing it describes is gone: "this
// device was online at 14:02, then it was removed". The original schema combined
// NOT NULL with ON DELETE SET NULL, so deleting a device either failed or lost
// its events.
func TestHistorySurvivesDeviceDeletion(t *testing.T) {
	store := openStore(t)
	if err := store.UpsertDevice(&model.Device{
		ID: "dev-gone", Name: "ephemeral", Type: model.DeviceTypeSmartphone,
		State: model.StateOnline,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := store.SaveEvent(&model.NetworkEvent{
		EventType: model.EventDeviceDiscovered,
		DeviceID:  "dev-gone",
		Message:   "appeared",
	}); err != nil {
		t.Fatalf("save event: %v", err)
	}

	if err := store.DeleteDevice("dev-gone"); err != nil {
		t.Fatalf("delete device: %v", err)
	}

	events, total, err := store.ListEvents(database.EventFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if total != 1 {
		t.Fatalf("after deleting the device there are %d events, want the event to survive", total)
	}
	if events[0].Message != "appeared" {
		t.Errorf("event message = %q", events[0].Message)
	}
	if events[0].DeviceID != "" {
		t.Errorf("event still references %q, want the reference cleared", events[0].DeviceID)
	}
}
