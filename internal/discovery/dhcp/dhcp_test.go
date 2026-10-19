package dhcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "easepi-r1", name))
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	return p
}

func newFixtureCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewWithPath(fixture(t, "dhcp-leases.txt"))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestCollectLeases(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 7 {
		t.Fatalf("expected 7 leases, got %d", len(results))
	}

	type lease struct {
		address  string
		hostname string
		clientID string
	}
	byMAC := map[string]lease{}
	for i := range results {
		r := &results[i]
		if r.Kind != "lease" || r.Lease == nil {
			t.Fatalf("unexpected result kind %q", r.Kind)
		}
		if r.Source != model.SourceDHCP {
			t.Fatalf("source = %q", r.Source)
		}
		byMAC[r.Lease.MAC] = lease{r.Lease.Address, r.Lease.Hostname, r.Lease.ClientID}
	}

	// The Windows PC has an explicit client id, which is a strong identity.
	pc, ok := byMAC["02:11:22:33:44:b7"]
	if !ok {
		t.Fatal("client-desktop lease missing")
	}
	if pc.address != "192.0.2.116" || pc.hostname != "client-desktop" {
		t.Errorf("client-desktop lease: %+v", pc)
	}
	if pc.clientID != "01:02:11:22:33:44:b7" {
		t.Errorf("client-desktop client id = %q", pc.clientID)
	}

	// A "*" client id is not an identity: it must be normalised to empty so
	// the identity engine does not treat it as a shared strong identifier.
	for _, mac := range []string{
		"06:77:88:99:aa:d2", "07:88:99:aa:bb:5e", "04:66:77:88:99:e3",
		"05:22:33:44:55:2d", "04:11:22:33:44:24", "cc:11:22:33:44:20",
	} {
		l, ok := byMAC[mac]
		if !ok {
			t.Fatalf("lease for %s missing", mac)
		}
		if l.clientID != "" {
			t.Errorf("%s: wildcard client id must be empty, got %q", mac, l.clientID)
		}
	}

	// The fixture's leases expired long ago; the collector still reports them
	// and marks them expired rather than dropping them.
	lamp := byMAC["05:22:33:44:55:2d"]
	if lamp.hostname != "smart-lamp" {
		t.Errorf("smart-lamp hostname = %q", lamp.hostname)
	}
	anon := byMAC["04:11:22:33:44:24"]
	if anon.hostname != "" {
		t.Errorf("expected empty hostname for '*', got %q", anon.hostname)
	}
}

func TestCollectExpiredLeases(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		r := &results[i]
		if r.Lease.ExpiresAt.IsZero() {
			t.Errorf("%s: no expiry recorded", r.Lease.MAC)
		}
		// The expired flag must be derived from the lease's own expiry against
		// the observation time, not hard coded.
		want := "true"
		if r.Lease.ExpiresAt.After(r.ObservedAt) {
			want = "false"
		}
		if got := r.Attributes["expired"]; got != want {
			t.Errorf("%s: expired = %q, want %q (expiry %s, observed %s)",
				r.Lease.MAC, got, want, r.Lease.ExpiresAt, r.ObservedAt)
		}
	}
}

// A missing lease file is a normal state (no DHCP server on this host), not an
// error, and makes the collector unsupported.
func TestMissingLeaseFile(t *testing.T) {
	c := NewWithPath(filepath.Join(t.TempDir(), "no-such-leases"))
	if c.Supported() {
		t.Fatal("a missing lease file must be unsupported")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect must not error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// An empty lease file is a valid state: the server has handed out nothing yet.
func TestEmptyLeaseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// A malformed line is skipped, not fatal: one bad row must not hide the others.
func TestMalformedLeaseLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases")
	content := "not-a-timestamp aa:bb:cc:dd:ee:ff 10.0.0.1 host *\n" +
		"1771000000 02:11:22:33:44:b7 192.0.2.116 client-desktop 01:02:11:22:33:44:b7\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 valid lease, got %d", len(results))
	}
	if results[0].Lease.MAC != "02:11:22:33:44:b7" {
		t.Fatalf("unexpected lease %q", results[0].Lease.MAC)
	}
}

// A lease never implies offline: the collector must not emit any device state.
func TestLeaseIsNotALivenessSignal(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		if results[i].Kind != "lease" {
			t.Errorf("lease collector emitted a %q result", results[i].Kind)
		}
	}
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorDHCP {
		t.Fatalf("name = %q", c.Name())
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
}
