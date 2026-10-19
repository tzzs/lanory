package topology

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/device"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/arp"
	"github.com/lanory/lanory/internal/discovery/dhcp"
	"github.com/lanory/lanory/internal/discovery/fdb"
	"github.com/lanory/lanory/internal/discovery/lldp"
	"github.com/lanory/lanory/internal/discovery/mdns"
	"github.com/lanory/lanory/internal/discovery/ndp"
	"github.com/lanory/lanory/internal/discovery/openwrt"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// apiTopologyProvider mirrors the narrow interface internal/api declares for the
// topology engine (see internal/api/providers.go).
//
// It is duplicated here on purpose: the API package must not import this one, and
// this package must not import the API. Declaring the shape in both places means
// a change on either side fails to compile here instead of at integration time.
type apiTopologyProvider interface {
	Build() (*model.Topology, error)
	Evidence() (*model.TopologyEvidence, error)
	AutoLayout(algorithm string) ([]model.TopologyLayout, error)
}

// TestWiringSatisfiesAPI proves the concrete service satisfies the contract the
// HTTP layer injects it through.
func TestWiringSatisfiesAPI(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "wiring.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)

	var svc apiTopologyProvider = NewService(store, config.Default())
	if svc == nil {
		t.Fatal("service is nil")
	}
	if _, err := svc.Build(); err != nil {
		t.Fatalf("Build through the API interface: %v", err)
	}
	if _, err := svc.Evidence(); err != nil {
		t.Fatalf("Evidence through the API interface: %v", err)
	}
}

// TestWiringWithRealEngines wires the real store, bus, identity engine and
// topology service exactly the way cmd/lanory does, and confirms the update
// engine accepts the identity resolver.
//
// It fails to compile if any of those stops satisfying the narrow interfaces this
// package declares.
func TestWiringWithRealEngines(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "wiring2.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := database.NewStore(db)

	bus := events.New(64)
	identity := device.New(store, bus)

	// The topology Builder reads through its own StoreReader.
	var _ StoreReader = store
	// The update engine accepts the identity engine as its resolver.
	var _ DeviceIngestor = ingestorAdapter{identity}
	var _ Publisher = bus

	svc := NewService(store, config.Default())
	if _, err := svc.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// A device created through the real identity engine appears in the graph.
	dev, created, err := identity.Resolve(device.Ingest{
		MAC:      "b0:82:e2:61:3e:b7",
		Hostname: "WindowsPC",
		Source:   model.SourceDHCP,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !created {
		t.Fatal("expected a new device")
	}
	topo, err := svc.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var found bool
	for _, n := range topo.Nodes {
		if n.ID == deviceNodeID(dev.ID) {
			found = true
			if n.Device == nil || n.Device.Name != "WindowsPC" {
				t.Errorf("node = %+v", n)
			}
		}
	}
	if !found {
		t.Fatalf("the device created by the identity engine is missing from the graph: %+v", topo.Nodes)
	}
}

// ingestorAdapter converts the real identity engine into the update engine's
// resolver interface (DeviceIngestor).
//
// The two live in different packages with deliberately different field sets
// (internal/topology must not import internal/device's Ingest type, and
// internal/device must not import internal/topology). The production wiring does
// exactly this adaption; keeping it in a test proves the shapes line up.
type ingestorAdapter struct {
	engine *device.Engine
}

func (a ingestorAdapter) Resolve(ing deviceIngest) (*model.Device, bool, error) {
	return a.engine.Resolve(device.Ingest{
		MAC:          ing.MAC,
		Hostname:     ing.Hostname,
		Vendor:       ing.Vendor,
		Addresses:    ing.Addresses,
		Source:       ing.Source,
		ObservedAt:   ing.ObservedAt,
		StrongID:     ing.StrongID,
		StrongIDType: ing.StrongIDType,
		ClientID:     ing.ClientID,
		TypeHint:     ing.TypeHint,
	})
}

// TestWiringAllCollectorsRegister proves the concrete collectors satisfy
// discovery.Collector and that the registry accepts them the way cmd/lanory
// registers them.
//
// It lives in the topology package because a test inside internal/discovery
// cannot import the collector packages (that would be an import cycle). This
// package already imports them for the fixture replay, so the wiring assertion
// belongs here.
func TestWiringAllCollectorsRegister(t *testing.T) {
	cs := []discovery.Collector{
		openwrt.New(),
		fdb.New(),
		arp.New(),
		ndp.New(),
		dhcp.New(),
		lldp.New(),
		mdns.New(),
	}

	seen := map[string]bool{}
	for _, c := range cs {
		if c == nil {
			t.Fatal("a collector is nil")
		}
		if c.Name() == "" {
			t.Error("a collector has no name")
		}
		if seen[c.Name()] {
			t.Errorf("duplicate collector name %q", c.Name())
		}
		seen[c.Name()] = true
		if c.Description() == "" {
			t.Errorf("%s has no description", c.Name())
		}
		if len(c.Capabilities()) == 0 {
			t.Errorf("%s declares no capabilities", c.Name())
		}
	}

	for _, want := range []string{
		model.CollectorOpenWRT, model.CollectorBridgeFDB, model.CollectorARP,
		model.CollectorNDP, model.CollectorDHCP, model.CollectorLLDP,
		model.CollectorMDNS,
	} {
		if !seen[want] {
			t.Errorf("no collector is named %q", want)
		}
	}

	// An unsupported collector must collect nothing and return no error: that is
	// what lets the registry skip it without recording a failure.
	ctx := context.Background()
	for _, c := range cs {
		if c.Supported() {
			continue
		}
		results, err := c.Collect(ctx)
		if err != nil {
			t.Errorf("%s is unsupported but Collect returned %v", c.Name(), err)
		}
		if len(results) != 0 {
			t.Errorf("%s is unsupported but returned %d results", c.Name(), len(results))
		}
	}

	// The registry accepts all of them and reports a coherent status row each.
	reg := discovery.NewRegistry(config.Default())
	reg.RegisterAll(cs...)
	statuses := reg.Status()
	if len(statuses) != len(cs) {
		t.Fatalf("statuses = %d, want %d", len(statuses), len(cs))
	}
	for _, st := range statuses {
		if st.Enabled && !st.Supported {
			t.Errorf("%s is reported enabled but unsupported", st.Name)
		}
	}
}
