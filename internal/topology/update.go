package topology

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// UpdateEngine reconciles one discovery run into the store.
//
// The rules it exists to enforce:
//
//   - Discovery never deletes. A device that disappears from the neighbour table
//     is marked unverified, not removed: a device being asleep is not the same
//     as a device being gone, and a deleted device loses its name, its type and
//     its history.
//   - User intent wins. A manual device, a locked edge and a user-confirmed
//     relation are never overwritten by an automatic pass, whatever the fresh
//     evidence says.
//   - Every inferred edge carries its evidence. The raw collector line that
//     produced an edge is stored in discovery_evidence with
//     subject_type 'edge', so the UI can always answer "why does Lanory think
//     this?".
//   - Speed and state changes are detected and reported, not silently absorbed.
//
// The engine is deliberately idempotent: running it twice with the same
// observations leaves the store in the same state, and does not duplicate
// devices or edges.
type UpdateEngine struct {
	store *database.Store
	ident DeviceIngestor
	bus   Publisher

	// Now is the clock used for observations; overridden in tests.
	Now func() time.Time
}

// DeviceIngestor is the slice of device.Engine the update engine needs.
//
// Declared as an interface so the topology package does not depend on the
// identity engine's implementation, and so tests can substitute a simple
// resolver.
type DeviceIngestor interface {
	Resolve(ing deviceIngest) (*model.Device, bool, error)
}

// deviceIngest mirrors device.Ingest without importing internal/device.
//
// It duplicates the field set on purpose: internal/device imports
// internal/discovery, and importing it here would create a dependency cycle the
// moment discovery needs a topology type. The adapter in the wiring layer
// converts one to the other.
type deviceIngest struct {
	MAC          string
	Hostname     string
	Vendor       string
	Addresses    []discovery.AddressObservation
	Source       model.DiscoverySource
	ObservedAt   time.Time
	StrongID     string
	StrongIDType model.IdentityType
	ClientID     string
	TypeHint     model.DeviceType
}

// Publisher is the slice of the event bus the engine needs.
type Publisher interface {
	PublishTopologyUpdated(reason string)
	PublishLinkUpdated(e *model.TopologyEdge, reason string)
	PublishDeviceUpdated(d *model.Device, reason string)
}

// NewUpdateEngine returns an update engine writing through store.
func NewUpdateEngine(store *database.Store, bus Publisher) *UpdateEngine {
	return &UpdateEngine{store: store, bus: bus, Now: func() time.Time { return time.Now().UTC() }}
}

// WithIngestor injects the identity resolver. Without one the engine can still
// update interfaces and edges, but it cannot create devices.
func (u *UpdateEngine) WithIngestor(i DeviceIngestor) *UpdateEngine {
	u.ident = i
	return u
}

// UpdateResult counts what one reconciliation did.
type UpdateResult struct {
	// Observations is the number of discovery results processed.
	Observations int
	// DevicesCreated and DevicesUpdated count device writes.
	DevicesCreated int
	DevicesUpdated int
	// InterfacesUpdated counts interface writes.
	InterfacesUpdated int
	// EdgesCreated and EdgesUpdated count edge writes.
	EdgesCreated int
	EdgesUpdated int
	// EdgesUnverified counts edges that lost their evidence this run.
	EdgesUnverified int
	// EvidenceSaved counts discovery_evidence rows written.
	EvidenceSaved int
	// StateChanges lists the device state transitions detected.
	StateChanges []StateChange
	// SpeedChanges lists the link speed transitions detected.
	SpeedChanges []SpeedChange
	// Errors collects non-fatal per-observation problems.
	Errors []error
}

// StateChange is a detected device state transition.
type StateChange struct {
	DeviceID string
	Name     string
	Old      model.DeviceState
	New      model.DeviceState
	Reason   string
}

// SpeedChange is a detected interface speed transition.
type SpeedChange struct {
	InterfaceID string
	Name        string
	OldMbps     int
	NewMbps     int
}

// Update reconciles a discovery summary into the store.
//
// A failure on one observation is recorded and the rest continue: a single bad
// row must not cost the user the whole scan.
func (u *UpdateEngine) Update(ctx context.Context, summary *discovery.RunSummary) (*UpdateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if u.store == nil {
		return nil, errors.New("topology: update engine has no store")
	}
	res := &UpdateResult{}

	at := u.now()
	if summary != nil {
		// Group the observations by what they tell us, so the reconciliation
		// happens once per device rather than once per result.
		batch := newObservationBatch(summary.Results, at)
		res.Observations = len(summary.Results)

		if err := u.applyInterfaces(ctx, batch, res); err != nil {
			res.Errors = append(res.Errors, err)
		}
		if err := u.applyDevices(ctx, batch, res); err != nil {
			res.Errors = append(res.Errors, err)
		}
		if err := u.applyEdges(ctx, batch, res); err != nil {
			res.Errors = append(res.Errors, err)
		}
		if err := u.markStale(ctx, batch, at, res); err != nil {
			res.Errors = append(res.Errors, err)
		}
	}

	if u.bus != nil {
		u.bus.PublishTopologyUpdated(u.summaryReason(res))
	}
	return res, nil
}

// summaryReason renders a short cause string for the topology.updated event.
func (u *UpdateEngine) summaryReason(res *UpdateResult) string {
	parts := []string{"discovery"}
	if res.DevicesCreated > 0 {
		parts = append(parts, fmt.Sprintf("%d new", res.DevicesCreated))
	}
	if res.EdgesCreated > 0 {
		parts = append(parts, fmt.Sprintf("%d links", res.EdgesCreated))
	}
	if res.EdgesUnverified > 0 {
		parts = append(parts, fmt.Sprintf("%d unverified", res.EdgesUnverified))
	}
	return strings.Join(parts, ", ")
}

func (u *UpdateEngine) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now().UTC()
}

// ---------------------------------------------------------------------------
// observation batch
// ---------------------------------------------------------------------------

// observationBatch groups the raw results by the entity they describe.
type observationBatch struct {
	interfaces []discovery.InterfaceObservation
	addresses  []discovery.AddressObservation
	neighbors  []discovery.NeighborObservation
	fdb        []discovery.FDBObservation
	leases     []discovery.LeaseObservation
	links      []discovery.LinkObservation
	generic    []discovery.Result

	// byMAC collects everything known about one MAC address.
	byMAC map[string]*macFacts

	at time.Time
}

// macFacts is everything the run learned about a single MAC.
type macFacts struct {
	MAC      string
	Hostname string
	ClientID string
	Router   bool

	// Ports lists every (bridge, port) the MAC was learned on.
	Ports []macPort
	// Addresses are the addresses the MAC owns.
	Addresses []discovery.AddressObservation
	// Sources are the collectors that observed it.
	Sources map[model.DiscoverySource]bool
	// LastSeen is the newest observation timestamp.
	LastSeen time.Time
	// Lease is the DHCP lease, when there is one.
	Lease *discovery.LeaseObservation
	// NeighborState is the strongest neighbour state observed.
	NeighborState model.NeighborState
	// Router marks an IPv6 neighbour that advertises itself as a router.
	RouterFlag bool
	// Evidence is the raw lines that produced the facts.
	Evidence []evidenceLine
	// deviceID is filled in once the identity engine has resolved the MAC.
	deviceID string
}

// macPort is one (bridge, port, speed) observation.
type macPort struct {
	Bridge    string
	Port      string
	SpeedMbps int
	VLANID    int
	EntryType model.FDBEntryType
	Source    model.DiscoverySource
	Observed  time.Time
}

// evidenceLine is the raw text that supports one inference.
type evidenceLine struct {
	Collector    string
	EvidenceType string
	Raw          string
	ObservedAt   time.Time
}

// newObservationBatch groups the results.
func newObservationBatch(results []discovery.Result, at time.Time) *observationBatch {
	b := &observationBatch{byMAC: map[string]*macFacts{}, at: at}
	for _, r := range results {
		switch {
		case r.Interface != nil:
			b.interfaces = append(b.interfaces, *r.Interface)
		case r.Address != nil:
			b.addresses = append(b.addresses, *r.Address)
			if r.Address.MAC != "" {
				b.mac(r.Address.MAC, r.Source, r.ObservedAt).addAddress(*r.Address)
			}
		case r.Neighbor != nil:
			b.neighbors = append(b.neighbors, *r.Neighbor)
			if r.Neighbor.MAC != "" {
				f := b.mac(r.Neighbor.MAC, r.Source, r.ObservedAt)
				f.addAddress(discovery.AddressObservation{
					Address:   r.Neighbor.Address,
					Family:    r.Neighbor.Family,
					Interface: r.Neighbor.Interface,
					MAC:       r.Neighbor.MAC,
				})
				f.NeighborState = strongerState(f.NeighborState, r.Neighbor.State)
				if r.Neighbor.Router {
					f.RouterFlag = true
				}
				f.addEvidence(evidenceLine{
					Collector:    collectorFor(r.Source),
					EvidenceType: "neighbor",
					Raw:          fmt.Sprintf("%s dev %s lladdr %s %s", r.Neighbor.Address, r.Neighbor.Interface, r.Neighbor.MAC, r.Neighbor.State),
					ObservedAt:   nonzero(r.ObservedAt, at),
				})
			}
		case r.FDBEntry != nil:
			b.fdb = append(b.fdb, *r.FDBEntry)
			if r.FDBEntry.MAC != "" {
				f := b.mac(r.FDBEntry.MAC, r.Source, r.ObservedAt)
				f.Ports = append(f.Ports, macPort{
					Bridge:    r.FDBEntry.Bridge,
					Port:      r.FDBEntry.Port,
					VLANID:    r.FDBEntry.VLANID,
					EntryType: r.FDBEntry.EntryType,
					Source:    r.Source,
					Observed:  nonzero(r.ObservedAt, at),
				})
				f.addEvidence(evidenceLine{
					Collector:    model.CollectorBridgeFDB,
					EvidenceType: "fdb",
					Raw:          fmt.Sprintf("%s dev %s master %s %s", r.FDBEntry.MAC, r.FDBEntry.Port, r.FDBEntry.Bridge, r.FDBEntry.EntryType),
					ObservedAt:   nonzero(r.ObservedAt, at),
				})
			}
		case r.Lease != nil:
			b.leases = append(b.leases, *r.Lease)
			if r.Lease.MAC != "" {
				f := b.mac(r.Lease.MAC, r.Source, r.ObservedAt)
				f.Lease = r.Lease
				if r.Lease.Hostname != "" {
					f.Hostname = r.Lease.Hostname
				}
				if r.Lease.ClientID != "" {
					f.ClientID = r.Lease.ClientID
				}
				f.addAddress(discovery.AddressObservation{
					Address: r.Lease.Address,
					Family:  model.AddressFamilyIPv4,
					MAC:     r.Lease.MAC,
				})
				f.addEvidence(evidenceLine{
					Collector:    model.CollectorDHCP,
					EvidenceType: "lease",
					Raw:          fmt.Sprintf("%s %s %s", r.Lease.MAC, r.Lease.Address, r.Lease.Hostname),
					ObservedAt:   nonzero(r.ObservedAt, at),
				})
			}
		case r.Link != nil:
			b.links = append(b.links, *r.Link)
			if r.Link.RemoteMAC != "" {
				f := b.mac(r.Link.RemoteMAC, r.Source, r.ObservedAt)
				if r.Link.Port != "" {
					f.Ports = append(f.Ports, macPort{
						Bridge:   r.Link.Interface,
						Port:     r.Link.Port,
						Source:   r.Source,
						Observed: nonzero(r.ObservedAt, at),
					})
				}
			}
		default:
			b.generic = append(b.generic, r)
			// LLDP and mDNS generic results carry a chassis MAC in their
			// attributes; pick it up so those sources contribute to the device.
			if mac := r.Attributes["chassis_mac"]; mac != "" {
				f := b.mac(mac, r.Source, r.ObservedAt)
				if name := r.Attributes["system_name"]; name != "" {
					f.Hostname = name
				}
				f.addEvidence(evidenceLine{
					Collector:    collectorFor(r.Source),
					EvidenceType: "lldp",
					Raw:          fmt.Sprintf("%s chassis %s port %s", mac, r.Attributes["chassis_id"], r.Attributes["port_id"]),
					ObservedAt:   nonzero(r.ObservedAt, at),
				})
			}
			if host := r.Attributes["hostname"]; host != "" {
				if mac := r.Attributes["mac"]; mac != "" {
					b.mac(mac, r.Source, r.ObservedAt).Hostname = host
				}
			}
		}
	}
	return b
}

// mac returns the facts for a MAC, creating them on first use.
//
// The key is the canonical 12-hex-digit form so the batch agrees with the
// database's own MAC normalisation and with the local-interface filter. Using
// the colon separated spelling would silently fail every such comparison.
func (b *observationBatch) mac(mac string, src model.DiscoverySource, at time.Time) *macFacts {
	key := database.CanonicalizeMAC(mac)
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(mac))
	}
	f, ok := b.byMAC[key]
	if !ok {
		f = &macFacts{MAC: key, Sources: map[model.DiscoverySource]bool{}}
		b.byMAC[key] = f
	}
	f.Sources[src] = true
	if at.After(f.LastSeen) {
		f.LastSeen = at
	}
	return f
}

func (f *macFacts) addAddress(a discovery.AddressObservation) {
	for _, existing := range f.Addresses {
		if existing.Address == a.Address {
			return
		}
	}
	f.Addresses = append(f.Addresses, a)
}

func (f *macFacts) addEvidence(e evidenceLine) {
	f.Evidence = append(f.Evidence, e)
}

// sortedMACs returns the MACs in a stable order.
func (b *observationBatch) sortedMACs() []string {
	out := make([]string, 0, len(b.byMAC))
	for mac := range b.byMAC {
		out = append(out, mac)
	}
	sort.Strings(out)
	return out
}

// strongerState keeps the more informative of two neighbour states.
//
// "Informative" means further from "we know nothing": REACHABLE beats STALE
// beats FAILED, and NONE is never an improvement. The point is that a device
// seen both REACHABLE (on IPv4) and STALE (on IPv6) is reachable.
func strongerState(a, b model.NeighborState) model.NeighborState {
	rank := func(s model.NeighborState) int {
		switch s {
		case model.NeighborReachable, model.NeighborPermanent:
			return 4
		case model.NeighborDelay, model.NeighborProbe:
			return 3
		case model.NeighborStale:
			return 2
		case model.NeighborFailed:
			return 1
		case model.NeighborIncomplete:
			return 0
		}
		return -1
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func collectorFor(src model.DiscoverySource) string {
	switch src {
	case model.SourceARP:
		return model.CollectorARP
	case model.SourceNDP:
		return model.CollectorNDP
	case model.SourceDHCP:
		return model.CollectorDHCP
	case model.SourceBridgeFDB:
		return model.CollectorBridgeFDB
	case model.SourceLLDP:
		return model.CollectorLLDP
	case model.SourceMDNS:
		return model.CollectorMDNS
	case model.SourceOpenWRT, model.SourceUCI, model.SourceUBUS:
		return model.CollectorOpenWRT
	}
	return string(src)
}

func nonzero(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
