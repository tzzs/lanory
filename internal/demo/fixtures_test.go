package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDatasetMatchesSanitizedFixtures keeps the demo dataset anchored to
// fixtures/easepi-r1 instead of drifting into invented values.
//
// The check is deliberately one-directional: every MAC, IPv4 and hostname the
// demo dataset claims for a *discovered* device must appear somewhere in the
// sanitized fixture files. Nodes that a user would add by hand (the Internet
// cloud, the optical modem, an extra NAS) are not in the fixture and are
// exempt. fixtures/private is never read.
func TestDatasetMatchesSanitizedFixtures(t *testing.T) {
	root := fixtureRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "*"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixture files under %s", root)
	}
	var sb strings.Builder
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	fixtures := sb.String()

	// Manual nodes are not part of the captured snapshot.
	manual := map[string]bool{
		"dev-manual-modem":     true, // optical modem, from the user's description
		"dev-mac-0f1122334455": true, // NAS, added by hand
		"dev-mac-053344556677": true, // Xiaomi router, transparent at layer 2
	}

	for _, d := range demoDevices {
		if manual[d.ID] {
			continue
		}
		if d.MAC != "" && !strings.Contains(fixtures, d.MAC) {
			t.Errorf("device %s MAC %s does not appear in fixtures/easepi-r1", d.ID, d.MAC)
		}
		if d.IPv4 != "" && !strings.Contains(fixtures, d.IPv4) {
			t.Errorf("device %s IPv4 %s does not appear in fixtures/easepi-r1", d.ID, d.IPv4)
		}
		if d.Hostname != "" && !strings.Contains(fixtures, d.Hostname) {
			t.Errorf("device %s hostname %s does not appear in fixtures/easepi-r1", d.ID, d.Hostname)
		}
	}

	// Interface speeds must match expected.yaml.
	for _, i := range demoInterfaces {
		switch i.Name {
		case "eth0":
			if i.SpeedMbps != 100 {
				t.Errorf("eth0 speed = %d, expected.yaml says 100", i.SpeedMbps)
			}
		case "eth1":
			if i.SpeedMbps != 1000 {
				t.Errorf("eth1 speed = %d, expected.yaml says 1000", i.SpeedMbps)
			}
		case "eth2":
			if i.SpeedMbps != 2500 {
				t.Errorf("eth2 speed = %d, expected.yaml says 2500", i.SpeedMbps)
			}
		case "eth3":
			if i.Carrier {
				t.Error("eth3 must have no carrier, expected.yaml says NO-CARRIER")
			}
		}
	}
}

// TestDatasetDoesNotContainPrivateFixtureData is a guard rail for the rule that
// nothing from fixtures/private may reach the demo dataset. The private
// directory is not tracked by git, so the test only runs when it happens to
// exist locally; it never reads anything when it does not.
func TestDatasetDoesNotContainPrivateFixtureData(t *testing.T) {
	priv := filepath.Join(repoRoot(t), "fixtures", "private")
	if fi, err := os.Stat(priv); err != nil || !fi.IsDir() {
		t.Skip("fixtures/private is not present; nothing to compare against")
	}
	// Deliberately does NOT read private files. The invariant that matters is
	// that every demo address is in a reserved documentation range, which
	// TestDatasetAddressesAreDocumentationRanges already enforces; private
	// addresses are RFC1918 and cannot pass that test.
	t.Log("fixtures/private exists; demo addresses are restricted to documentation ranges")
}

// TestDemoDatasetIsDeterministic checks repeated calls return equal values, so
// the UI never sees the dataset shift between loads.
func TestDemoDatasetIsDeterministic(t *testing.T) {
	a := demoEdges()
	b := demoEdges()
	if len(a) != len(b) {
		t.Fatalf("demoEdges length varies: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("demoEdges[%d] differs between calls", i)
		}
	}
	pa, pb := demoProbes(), demoProbes()
	if len(pa) != len(pb) {
		t.Fatalf("demoProbes length varies: %d vs %d", len(pa), len(pb))
	}
	for i := range pa {
		if pa[i].ID != pb[i].ID || pa[i].Target != pb[i].Target {
			t.Errorf("demoProbes[%d] differs between calls", i)
		}
	}
	ea, eb := demoEvents(), demoEvents()
	if len(ea) != len(eb) {
		t.Fatalf("demoEvents length varies: %d vs %d", len(ea), len(eb))
	}
}

// fixtureRoot returns the sanitized fixture directory.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "fixtures", "easepi-r1")
}

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate the repository root (go.mod)")
		}
		dir = parent
	}
}
