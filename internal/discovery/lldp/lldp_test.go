package lldp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// lldpJSON is a minimal `lldpcli show neighbors -f json` document.
const lldpJSON = `{
  "lldp": {
    "eth1": {
      "interface": {
        "Xiaomi Router": {
          "chassis": [
            {
              "id": {"type": "mac", "value": "b0:1b:cd:a2:33:11"},
              "name": "Xiaomi Router",
              "descr": "Xiaomi Router AX9000"
            }
          ],
          "port": [
            {
              "id": {"type": "ifname", "value": "wan"},
              "descr": "uplink to EASEPi"
            }
          ]
        }
      }
    },
    "eth2": {
      "interface": {
        "client-desktop": {
          "chassis": [
            {
              "id": {"type": "local", "value": "client-desktop"},
              "name": "client-desktop"
            }
          ],
          "port": [
            {"id": {"type": "local", "value": "eth0"}}
          ]
        }
      }
    }
  }
}`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lldp.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func newFixtureCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewWithPath(writeTemp(t, lldpJSON))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestCollectNeighbours(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	var (
		links   []*discoveryLink
		generic int
	)
	for i := range results {
		r := &results[i]
		switch {
		case r.Link != nil:
			links = append(links, &discoveryLink{
				iface:      r.Link.Interface,
				port:       r.Link.Port,
				localMAC:   r.Link.LocalMAC,
				linkType:   r.Link.LinkType,
				confidence: r.Link.Confidence,
				systemName: r.Link.Extra["system_name"],
			})
		case r.Kind == "generic":
			generic++
		}
	}

	if len(links) != 2 {
		t.Fatalf("expected 2 LLDP links, got %d", len(links))
	}
	if generic != 2 {
		t.Fatalf("expected 2 generic observations, got %d", generic)
	}

	// The Xiaomi router is a confirmed physical neighbour on eth1, and LLDP is
	// the highest confidence source there is.
	xiaomi := links[0]
	if xiaomi.iface != "eth1" {
		t.Fatalf("first link interface = %q", xiaomi.iface)
	}
	if xiaomi.localMAC != "b0:1b:cd:a2:33:11" {
		t.Errorf("chassis mac = %q", xiaomi.localMAC)
	}
	if xiaomi.port != "wan" {
		t.Errorf("port = %q", xiaomi.port)
	}
	if xiaomi.linkType != model.EdgeTypePhysicalLink {
		t.Errorf("link type = %q, want physical_link", xiaomi.linkType)
	}
	if xiaomi.confidence != 0.95 {
		t.Errorf("confidence = %v, want 0.95", xiaomi.confidence)
	}
	if xiaomi.systemName != "Xiaomi Router" {
		t.Errorf("system name = %q", xiaomi.systemName)
	}
}

// LLDP must never be reported when the binary is absent: that is the whole
// point of Supported().
func TestUnsupportedWhenNoBinary(t *testing.T) {
	c := New()
	if c.Supported() {
		t.Skip("lldpcli is installed on this host")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestMalformedJSON(t *testing.T) {
	c := NewWithPath(writeTemp(t, "{not json"))
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("malformed JSON must not fail the collector: %v", err)
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

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorLLDP {
		t.Fatalf("name = %q", c.Name())
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
}

type discoveryLink struct {
	iface      string
	port       string
	localMAC   string
	linkType   model.EdgeType
	confidence float64
	systemName string
}
