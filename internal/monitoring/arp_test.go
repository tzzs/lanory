package monitoring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

const arpFixture = `IP address       HW type     Flags       HW address            Mask     Device
192.168.100.1    0x1         0x2         b0:1b:cd:a2:33:10     *        eth0
192.168.100.116  0x1         0x2         b0:82:e2:61:3e:b7     *        eth2
192.168.100.222  0x1         0x2         86:7b:1c:3f:06:5e     *        eth1
192.168.100.253  0x1         0x0         00:00:00:00:00:00     *        eth1
`

func writeARP(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arp")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestARPObserverParsesKernelTable checks the parsing of /proc/net/arp.
func TestARPObserverParsesKernelTable(t *testing.T) {
	a := NewARPObserverWithPath(writeARP(t, arpFixture))
	n, err := a.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n != 4 {
		t.Errorf("read %d entries, want 4", n)
	}

	entry, ok := a.Entry("192.168.100.116")
	if !ok {
		t.Fatal("192.168.100.116 is missing")
	}
	if entry.MAC != "b0:82:e2:61:3e:b7" {
		t.Errorf("mac = %q", entry.MAC)
	}
	if entry.Device != "eth2" {
		t.Errorf("device = %q, want eth2", entry.Device)
	}
}

// TestARPObserverIgnoresIncompleteEntries is the important one: the kernel
// keeps a row for an address it could not resolve, with a zero flag and a zero
// MAC. Treating that as "the host is there" would report a device as present
// when nothing answered.
func TestARPObserverIgnoresIncompleteEntries(t *testing.T) {
	a := NewARPObserverWithPath(writeARP(t, arpFixture))
	if _, err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if !a.Seen("192.168.100.116") {
		t.Error("a resolved entry must count as present")
	}
	if a.Seen("192.168.100.253") {
		t.Error("an unresolved entry must not count as present")
	}
	if a.Seen("192.168.100.99") {
		t.Error("an address that is not in the table must not count as present")
	}
}

// TestPresenceChangesOfflineIntoSleeping is the behaviour the specification
// asks for: a device that is present in the neighbour table but does not
// answer the probe method must not be reported as offline.
func TestPresenceChangesOfflineIntoSleeping(t *testing.T) {
	m := NewStateMachine(nil).WithPresence(&fakePresence{present: true})
	target := model.ProbeTarget{
		ID: "probe-1", DeviceID: "dev-1", Target: "192.168.100.116",
		Protocol: model.ProbeICMP, FailureThreshold: 3, RecoverConfirm: 1,
	}
	for i := 0; i < 3; i++ {
		m.Apply(target, false)
	}
	state := m.CurrentState("probe-1")
	if state != model.StateSleeping {
		t.Errorf("state = %q, want %q: a device in the neighbour table is not offline", state, model.StateSleeping)
	}

	// Without the cross-check the same history means offline.
	m2 := NewStateMachine(nil)
	for i := 0; i < 3; i++ {
		m2.Apply(target, false)
	}
	if got := m2.CurrentState("probe-1"); got != model.StateOffline {
		t.Errorf("state without the cross-check = %q, want %q", got, model.StateOffline)
	}
}

// fakePresence is a PresenceChecker with a fixed answer.
type fakePresence struct{ present bool }

func (f *fakePresence) Seen(string) bool { return f.present }
