package mdns

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// avahiOutput is the parseable (`-p`) output of
// `avahi-browse -a -t -r -p`, with a resolved service per host.
const avahiOutput = `+;eth1;IPv4;iPhone;_airplay._tcp;local
+;eth1;IPv4;iPhone;_raop._tcp;local
=;eth1;IPv4;iPhone;_airplay._tcp;local
iPhone;192.0.2.151;7000
 txtver=1
=;eth1;IPv4;iPhone;_raop._tcp;local
iPhone;192.0.2.151;5000
=;eth2;IPv4;client-desktop;_smb._tcp;local
client-desktop;192.0.2.116;445
=;eth1;IPv4;smart-lamp;_esphomelib._tcp;local
smart-lamp;192.0.2.159;6053
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "avahi.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func newFixtureCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewWithPath(writeTemp(t, avahiOutput))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestCollectAnnouncements(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	var (
		generic []map[string]string
		links   int
		addrs   int
		minConf = 2.0
	)
	for i := range results {
		r := &results[i]
		switch {
		case r.Kind == "generic":
			generic = append(generic, r.Attributes)
		case r.Link != nil:
			links++
			if r.Link.Confidence < minConf {
				minConf = r.Link.Confidence
			}
		case r.Address != nil:
			addrs++
		}
	}

	// Four distinct (host, service) pairs.
	if len(generic) != 4 {
		t.Fatalf("expected 4 service announcements, got %d: %v", len(generic), generic)
	}
	if addrs != 4 {
		t.Errorf("expected 4 address observations, got %d", addrs)
	}
	if links != 4 {
		t.Errorf("expected 4 link observations, got %d", links)
	}

	// mDNS is the lowest-priority source: its confidence must stay below every
	// other evidence class.
	if minConf != discovery.ConfidenceMDNS {
		t.Errorf("link confidence = %v, want %v", minConf, discovery.ConfidenceMDNS)
	}
	if discovery.ConfidenceMDNS >= discovery.ConfidenceFDB {
		t.Error("mDNS must rank below the FDB")
	}

	// The hostnames are what the identity engine uses to name devices.
	hosts := map[string]bool{}
	for _, a := range generic {
		hosts[a["hostname"]] = true
		if a["service"] == "" {
			t.Errorf("announcement without a service: %v", a)
		}
	}
	for _, want := range []string{"iPhone", "client-desktop", "smart-lamp"} {
		if !hosts[want] {
			t.Errorf("hostname %q missing from %v", want, hosts)
		}
	}
}

// The collector must not invent a MAC: mDNS has none.
func TestNoMACFromMDNS(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		r := &results[i]
		if r.Address != nil && r.Address.MAC != "" {
			t.Errorf("mDNS address observation invented a MAC: %q", r.Address.MAC)
		}
		if r.Link != nil && r.Link.RemoteMAC != "" {
			t.Errorf("mDNS link observation invented a MAC: %q", r.Link.RemoteMAC)
		}
	}
}

func TestUnsupportedWhenNoBrowser(t *testing.T) {
	c := New()
	if c.Supported() {
		t.Skip("an mDNS browser is installed on this host")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestEmptyInput(t *testing.T) {
	c := NewWithPath(writeTemp(t, ""))
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestParseAvahiBrowse(t *testing.T) {
	recs := ParseAvahiBrowse([]byte(avahiOutput))
	if len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d: %+v", len(recs), recs)
	}
	byHost := map[string]Record{}
	for _, r := range recs {
		byHost[r.Hostname] = r
	}
	pc, ok := byHost["client-desktop"]
	if !ok {
		t.Fatal("client-desktop record missing")
	}
	if pc.Address != "192.0.2.116" {
		t.Errorf("address = %q", pc.Address)
	}
	if pc.Port != 445 {
		t.Errorf("port = %d", pc.Port)
	}
	if pc.Service != "_smb._tcp" {
		t.Errorf("service = %q", pc.Service)
	}
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorMDNS {
		t.Fatalf("name = %q", c.Name())
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
}
