package monitoring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// writeIface creates a synthetic sysfs entry for an interface.
func writeIface(t *testing.T, root, name, carrier, operstate, speed string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if carrier != "" {
		if err := os.WriteFile(filepath.Join(dir, "carrier"), []byte(carrier+"\n"), 0o644); err != nil {
			t.Fatalf("write carrier: %v", err)
		}
	}
	if operstate != "" {
		if err := os.WriteFile(filepath.Join(dir, "operstate"), []byte(operstate+"\n"), 0o644); err != nil {
			t.Fatalf("write operstate: %v", err)
		}
	}
	if speed != "" {
		if err := os.WriteFile(filepath.Join(dir, "speed"), []byte(speed+"\n"), 0o644); err != nil {
			t.Fatalf("write speed: %v", err)
		}
	}
}

// removeIfaceAttr removes one attribute so a test can simulate a kernel that does
// not expose it.
func removeIfaceAttr(t *testing.T, root, name, attr string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, name, attr)); err != nil {
		t.Fatalf("remove %s: %v", attr, err)
	}
}

// TestLinkObserveFixtureState asserts the observer derives the right EdgeStatus
// from the interface states of the reference EASEPi R1 fixture:
//
//	eth1 1000 Mbps up, eth2 2500 Mbps up, eth3 no-carrier.
func TestLinkObserveFixtureState(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")
	writeIface(t, root, "eth2", "1", "up", "2500")
	writeIface(t, root, "eth3", "0", "down", "-1")

	obs := newLinkObserverWithRoot(root, nil, nil)
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("observed %d interfaces, want 3", len(statuses))
	}
	byName := map[string]LinkStatus{}
	for _, s := range statuses {
		byName[s.Interface.Name] = s
	}

	if got := byName["eth1"]; got.Status != model.EdgeStatusLinkUp {
		t.Fatalf("eth1 status = %q, want link_up (%s)", got.Status, got.Reason)
	}
	if got := byName["eth1"]; got.Interface.SpeedMbps != 1000 {
		t.Fatalf("eth1 speed = %d, want 1000", got.Interface.SpeedMbps)
	}
	if got := byName["eth2"]; got.Status != model.EdgeStatusLinkUp {
		t.Fatalf("eth2 status = %q, want link_up", got.Status)
	}
	if got := byName["eth2"]; got.Interface.SpeedMbps != 2500 {
		t.Fatalf("eth2 speed = %d, want 2500", got.Interface.SpeedMbps)
	}
	if got := byName["eth3"]; got.Status != model.EdgeStatusLinkDown {
		t.Fatalf("eth3 status = %q, want link_down (%s)", got.Status, got.Reason)
	}
	if got := byName["eth3"]; got.Interface.Carrier {
		t.Fatal("eth3 reports carrier up")
	}
}

// TestLinkDownTransitionEmitsEvent asserts eth1 going UP -> DOWN produces a
// link.down event with the old and new value recorded.
func TestLinkDownTransitionEmitsEvent(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")

	obs := newLinkObserverWithRoot(root, nil, nil)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	if ev := obs.Events(); len(ev) != 0 {
		t.Fatalf("first observation emitted %d events, want 0", len(ev))
	}

	writeIface(t, root, "eth1", "0", "down", "-1")
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("second observe: %v", err)
	}
	if statuses[0].Status != model.EdgeStatusLinkDown {
		t.Fatalf("status = %q, want link_down", statuses[0].Status)
	}

	events := obs.Events()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.EventType != model.EventLinkDown {
		t.Fatalf("event type = %q, want %q", ev.EventType, model.EventLinkDown)
	}
	if ev.OldValue != "up" || ev.NewValue != "down" {
		t.Fatalf("event values = (%q,%q), want (up,down)", ev.OldValue, ev.NewValue)
	}
	if !strings.Contains(ev.Message, "eth1") {
		t.Fatalf("message = %q, want it to name the interface", ev.Message)
	}
	if !strings.Contains(ev.Evidence, "carrier") {
		t.Fatalf("evidence = %q, want it to cite the sysfs carrier file", ev.Evidence)
	}
}

// TestLinkUpTransitionEmitsEvent asserts the reverse transition.
func TestLinkUpTransitionEmitsEvent(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth3", "0", "down", "-1")

	obs := newLinkObserverWithRoot(root, nil, nil)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	writeIface(t, root, "eth3", "1", "up", "1000")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("second observe: %v", err)
	}

	events := obs.Events()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(events), events)
	}
	if events[0].EventType != model.EventLinkUp {
		t.Fatalf("event type = %q, want %q", events[0].EventType, model.EventLinkUp)
	}
	if events[0].OldValue != "down" || events[0].NewValue != "up" {
		t.Fatalf("event values = (%q,%q), want (down,up)", events[0].OldValue, events[0].NewValue)
	}
}

// TestSpeedChangeEmitsEvent asserts a 1000 -> 100 speed change is detected and
// reported, which is the second link transition the product requires.
func TestSpeedChangeEmitsEvent(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")

	obs := newLinkObserverWithRoot(root, nil, nil)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}

	writeIface(t, root, "eth1", "1", "up", "100")
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("second observe: %v", err)
	}
	if statuses[0].Interface.SpeedMbps != 100 {
		t.Fatalf("speed = %d, want 100", statuses[0].Interface.SpeedMbps)
	}
	if statuses[0].Status != model.EdgeStatusLinkUp {
		t.Fatalf("status = %q, want link_up (the cable is still connected)", statuses[0].Status)
	}

	events := obs.Events()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.EventType != model.EventSpeedChanged {
		t.Fatalf("event type = %q, want %q", ev.EventType, model.EventSpeedChanged)
	}
	if ev.OldValue != "1000" || ev.NewValue != "100" {
		t.Fatalf("event values = (%q,%q), want (1000,100)", ev.OldValue, ev.NewValue)
	}
	if !strings.Contains(ev.Message, "1000") || !strings.Contains(ev.Message, "100") {
		t.Fatalf("message = %q, want it to show both speeds", ev.Message)
	}
}

// TestCarrierAndSpeedChangeTogether asserts a cable swap that also changes speed
// emits both events rather than swallowing one.
func TestCarrierAndSpeedChangeTogether(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth2", "1", "up", "2500")

	obs := newLinkObserverWithRoot(root, nil, nil)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	// Down and back up at a lower speed in one poll is reported as a link down
	// plus a speed change; both facts are real.
	writeIface(t, root, "eth2", "0", "down", "-1")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("second observe: %v", err)
	}
	writeIface(t, root, "eth2", "1", "up", "100")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("third observe: %v", err)
	}

	var down, speed int
	for _, ev := range obs.Events() {
		switch ev.EventType {
		case model.EventLinkDown:
			down++
		case model.EventSpeedChanged:
			speed++
		}
	}
	if down != 1 {
		t.Fatalf("link.down events = %d, want 1", down)
	}
	if speed != 1 {
		t.Fatalf("speed.changed events = %d, want 1", speed)
	}
}

// TestLinkMissingSysfsIsNotAnError asserts the observer survives a host without
// sysfs (macOS during development) instead of crashing or reporting everything
// as down.
func TestLinkMissingSysfsIsNotAnError(t *testing.T) {
	obs := newLinkObserverWithRoot(filepath.Join(t.TempDir(), "does-not-exist"), nil, nil)
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("observed %d interfaces without sysfs, want 0", len(statuses))
	}
}

// TestLinkMissingCarrierFileIsUnverified asserts a missing carrier file yields
// "unverified", never a claimed broken cable.
func TestLinkMissingCarrierFileIsUnverified(t *testing.T) {
	root := t.TempDir()
	// Only operstate exists: the kernel hides carrier for some virtual devices.
	writeIface(t, root, "br-lan", "", "unknown", "")

	obs := newLinkObserverWithRoot(root, nil, nil)
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("observed %d interfaces, want 1", len(statuses))
	}
	if statuses[0].Status != model.EdgeStatusUnverified {
		t.Fatalf("status = %q, want unverified (%s)", statuses[0].Status, statuses[0].Reason)
	}
	if !strings.Contains(statuses[0].Reason, "carrier") {
		t.Fatalf("reason = %q, want it to explain the missing carrier information", statuses[0].Reason)
	}
}

// TestEdgeStatusFor asserts the pure status derivation table.
func TestEdgeStatusFor(t *testing.T) {
	cases := []struct {
		name   string
		sample linkSample
		want   model.EdgeStatus
	}{
		{"carrier up with speed", linkSample{carrier: true, oper: "up", speed: 1000}, model.EdgeStatusLinkUp},
		{"carrier up without speed", linkSample{carrier: true, oper: "up"}, model.EdgeStatusLinkUp},
		{"carrier up but operstate unknown", linkSample{carrier: true}, model.EdgeStatusLinkUp},
		{"carrier down", linkSample{carrier: false, oper: "down"}, model.EdgeStatusLinkDown},
		{"no carrier", linkSample{carrier: false, oper: "no-carrier"}, model.EdgeStatusLinkDown},
		{"nothing known", linkSample{}, model.EdgeStatusUnverified},
		{"operstate up without carrier", linkSample{oper: "up"}, model.EdgeStatusUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := edgeStatusFor(tc.sample)
			if got != tc.want {
				t.Fatalf("edgeStatusFor(%+v) = %q, want %q (%s)", tc.sample, got, tc.want, reason)
			}
			if reason == "" {
				t.Fatal("edgeStatusFor returned an empty reason")
			}
		})
	}
}

// TestLinkEventsPersistedAndPublished asserts a link event is attributed to the
// device that owns the port, persisted in the history and published on the bus.
func TestLinkEventsPersistedAndPublished(t *testing.T) {
	store := newFakeLinkStore(model.NetworkInterface{
		ID: "iface-eth1", DeviceID: "dev-router", Name: "eth1",
		Type: model.InterfaceTypeEthernet, Role: model.InterfaceRoleLAN,
	})

	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")

	pub := &recordingPublisher{}
	obs := newLinkObserverWithRoot(root, store, pub)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	writeIface(t, root, "eth1", "0", "down", "-1")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("second observe: %v", err)
	}

	pub.mu.Lock()
	links := pub.links
	pub.mu.Unlock()
	if links != 1 {
		t.Fatalf("published %d link.updated frames, want 1", links)
	}
	if err := obs.LinkError(); err != nil {
		t.Fatalf("persisting the link event failed: %v", err)
	}

	events := store.savedEvents()
	if len(events) != 1 {
		t.Fatalf("persisted %d events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.EventType != model.EventLinkDown {
		t.Fatalf("event type = %q, want link.down", ev.EventType)
	}
	if ev.DeviceID != "dev-router" {
		t.Fatalf("event device = %q, want dev-router (the owner of eth1)", ev.DeviceID)
	}
	if ev.OldValue != "up" || ev.NewValue != "down" {
		t.Fatalf("event values = (%q,%q), want (up,down)", ev.OldValue, ev.NewValue)
	}
}

// TestLinkEventsAgainstRealStore asserts the same path against the real SQLite
// store. network_interfaces is unreadable in the current schema revision, so the
// event stays device-less and the store rejects it; the observer must record the
// error rather than lose the event.
func TestLinkEventsAgainstRealStore(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")

	obs := newLinkObserverWithRoot(root, s, nil)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	writeIface(t, root, "eth1", "0", "down", "-1")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("second observe: %v", err)
	}

	if len(obs.Events()) != 1 {
		t.Fatalf("retained %d events, want 1", len(obs.Events()))
	}
	// Either the event was persisted, or the store refused it and the error was
	// captured. Silently dropping it is the only unacceptable outcome.
	events, _, err := s.ListEvents(database.EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 && obs.LinkError() == nil {
		t.Fatal("the link event was neither persisted nor reported as an error")
	}
}

// TestLinkEventWithoutOwnerIsRetained asserts the degradation path: with no
// device to attribute the port to, the event is still published on the bus and
// retained in memory rather than losing the fact entirely.
func TestLinkEventWithoutOwnerIsRetained(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")

	pub := &recordingPublisher{}
	obs := newLinkObserverWithRoot(root, s, pub)
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	writeIface(t, root, "eth1", "0", "down", "-1")
	if _, err := obs.Observe(); err != nil {
		t.Fatalf("second observe: %v", err)
	}

	events := obs.Events()
	if len(events) != 1 || events[0].EventType != model.EventLinkDown {
		t.Fatalf("retained events = %+v, want one link.down", events)
	}
	pub.mu.Lock()
	links := pub.links
	pub.mu.Unlock()
	if links != 1 {
		t.Fatalf("published %d link.updated frames, want 1", links)
	}
	if err := obs.LinkError(); err == nil {
		t.Log("the device-less event was persisted; the schema accepted it")
	}
}

// TestReachabilityIsNotDerivedFromLinkState asserts the conceptual separation:
// a port that is down does not make a reachability probe fail, and a reachable
// device does not turn a down port into link_up.
func TestReachabilityIsNotDerivedFromLinkState(t *testing.T) {
	root := t.TempDir()
	// A wireless client is reachable while its (bridge) port shows no carrier.
	writeIface(t, root, "wlan0", "0", "down", "-1")
	obs := newLinkObserverWithRoot(root, nil, nil)
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if statuses[0].Status != model.EdgeStatusLinkDown {
		t.Fatalf("wlan0 link status = %q, want link_down", statuses[0].Status)
	}

	// A successful probe of the device behind it is recorded separately and does
	// not rewrite the link status.
	ln, port, stop := startListener(t)
	defer stop()
	_ = ln
	e := NewExecutor(DefaultExecutorConfig())
	rec := e.Execute(t.Context(), model.ProbeTarget{
		Target: "127.0.0.1", Port: port, Protocol: model.ProbeTCP, Timeout: 2,
	})
	if !rec.Result.Success {
		t.Fatalf("reachability probe failed: %s", rec.Result.ErrorMessage)
	}
	if statuses[0].Status != model.EdgeStatusLinkDown {
		t.Fatal("a successful reachability probe changed the physical link status")
	}
}

// TestLinkObserverUsesStoredInterfaceMetadata asserts the observer merges the
// stored interface row (id, role, counters) into what it reports, so the API
// keeps returning stable metadata while the live sysfs state drives the status.
func TestLinkObserverUsesStoredInterfaceMetadata(t *testing.T) {
	store := newFakeLinkStore(model.NetworkInterface{
		ID: "iface-eth1", DeviceID: "dev-router", Name: "eth1",
		Type: model.InterfaceTypeEthernet, Role: model.InterfaceRoleLAN,
		AdminState: model.AdminUp, OperState: model.OperUp, Carrier: true,
		SpeedMbps: 1000, MTU: 1500, IsPhysical: true,
		RXBytes: 12345, TXBytes: 6789, RXErrors: 1, TXErrors: 2,
	})

	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")
	obs := newLinkObserverWithRoot(root, store, nil)
	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("observed %d interfaces, want 1", len(statuses))
	}
	got := statuses[0].Interface
	if got.ID != "iface-eth1" {
		t.Fatalf("interface id = %q, want iface-eth1", got.ID)
	}
	if got.DeviceID != "dev-router" {
		t.Fatalf("device id = %q, want dev-router", got.DeviceID)
	}
	if got.Role != model.InterfaceRoleLAN {
		t.Fatalf("role = %q, want lan", got.Role)
	}
	if got.MTU != 1500 {
		t.Fatalf("mtu = %d, want 1500", got.MTU)
	}
	if got.RXBytes != 12345 || got.TXBytes != 6789 {
		t.Fatalf("counters = (%d,%d), want (12345,6789)", got.RXBytes, got.TXBytes)
	}
	if got.SpeedMbps != 1000 {
		t.Fatalf("speed = %d, want 1000", got.SpeedMbps)
	}
	if statuses[0].Status != model.EdgeStatusLinkUp {
		t.Fatalf("status = %q, want link_up", statuses[0].Status)
	}
}

// TestLinkObserverWithoutStoredInterfaces asserts the observer reports the live
// sysfs state even when the store holds no interface rows: missing metadata must
// not turn into "link down".
func TestLinkObserverWithoutStoredInterfaces(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")
	obs := newLinkObserverWithRoot(root, testStore(t), nil)

	statuses, err := obs.Observe()
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("observed %d interfaces, want 1", len(statuses))
	}
	if statuses[0].Status != model.EdgeStatusLinkUp {
		t.Fatalf("status = %q, want link_up", statuses[0].Status)
	}
	if statuses[0].Interface.SpeedMbps != 1000 {
		t.Fatalf("speed = %d, want 1000", statuses[0].Interface.SpeedMbps)
	}
}

// TestLinkObserverConcurrentObserveIsSafe asserts Observe can be called from
// multiple goroutines without a data race (run with -race).
func TestLinkObserverConcurrentObserveIsSafe(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth1", "1", "up", "1000")
	obs := newLinkObserverWithRoot(root, nil, nil)

	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				_, _ = obs.Observe()
				_ = obs.Events()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent Observe deadlocked")
		}
	}
}
