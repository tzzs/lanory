// Package discovery defines the Collector contract shared by every discovery
// source, plus the normalized Result type they all emit.
//
// Design rules that every collector must follow:
//
//   - A collector never fails the whole discovery run. It returns an error that
//     is recorded and the other collectors continue.
//   - A collector only *reports* observations. It must not mutate the network.
//   - When the underlying system command or file is missing, the collector
//     reports Supported() == false instead of erroring.
//   - Every result carries its source, timestamp, evidence and a confidence so
//     the topology engine can reason about it.
package discovery

import (
	"context"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// Collector is the interface every discovery source implements.
type Collector interface {
	// Name returns the stable collector identifier, e.g. "bridge_fdb".
	Name() string
	// Supported reports whether the collector can run on this host. A
	// collector that is not supported is skipped silently.
	Supported() bool
	// Capabilities lists what this collector can provide, e.g. "mac", "ipv4".
	Capabilities() []string
	// Collect runs the collector and returns normalized results.
	Collect(ctx context.Context) ([]Result, error)
	// Description returns a short human readable explanation.
	Description() string
}

// Result is one normalized discovery observation.
//
// A single collector invocation typically produces many results: one per
// observed MAC, per address, per interface and per link.
type Result struct {
	// Kind selects which payload fields are populated.
	Kind ResultKind

	// Source is the collector that produced this result.
	Source model.DiscoverySource
	// ObservedAt is when the observation was made.
	ObservedAt time.Time

	// --- interface observations ---
	Interface *InterfaceObservation

	// --- address observations ---
	Address *AddressObservation

	// --- neighbour (ARP/NDP) observations ---
	Neighbor *NeighborObservation

	// --- FDB observations ---
	FDBEntry *FDBObservation

	// --- link observations ---
	Link *LinkObservation

	// --- DHCP lease observations ---
	Lease *LeaseObservation

	// --- raw passthrough (LLDP, mDNS, SSDP...) ---
	Attributes map[string]string
}

// ResultKind selects the payload of a Result.
type ResultKind string

const (
	ResultInterface ResultKind = "interface"
	ResultAddress   ResultKind = "address"
	ResultNeighbor  ResultKind = "neighbor"
	ResultFDB       ResultKind = "fdb"
	ResultLink      ResultKind = "link"
	ResultLease     ResultKind = "lease"
	ResultGeneric   ResultKind = "generic"
)

// InterfaceObservation describes a network interface and its role.
type InterfaceObservation struct {
	Name       string
	MAC        string
	Type       model.InterfaceType
	Role       model.InterfaceRole
	AdminState model.AdminState
	OperState  model.OperState
	Carrier    bool
	SpeedMbps  int
	MTU        int
	BridgeID   string
	VLANID     int
	IsPhysical bool

	// Addresses attached to the interface, if any.
	Addresses []AddressObservation

	// Stats are the interface counters.
	RXBytes   uint64
	TXBytes   uint64
	RXErrors  uint64
	TXErrors  uint64
	RXDropped uint64
	TXDropped uint64
	RXPackets uint64
	TXPackets uint64

	// Virtual marks interfaces that are not physical network ports (docker0,
	// dummy0, gretap0, erspan0, ...). Virtual interfaces are recorded for
	// advanced inspection but never treated as end-device attachment points.
	Virtual bool

	// Ignore marks interfaces that must be skipped entirely (lo, ...).
	Ignore bool
}

// AddressObservation describes an IP address seen on an interface or a device.
type AddressObservation struct {
	Address      string
	Family       model.AddressFamily
	PrefixLength int
	MAC          string // owning MAC when known
	Interface    string // interface name
	Hostname     string
}

// NeighborObservation describes an ARP/NDP neighbour entry.
type NeighborObservation struct {
	Address   string
	Family    model.AddressFamily
	MAC       string
	Interface string
	State     model.NeighborState
	// Router marks IPv6 neighbours that advertise themselves as routers.
	Router bool
}

// FDBObservation describes a bridge forwarding database entry.
type FDBObservation struct {
	MAC       string
	Bridge    string
	Port      string
	VLANID    int
	EntryType model.FDBEntryType
	// Self marks the bridge's own address on one of its ports.
	Self bool
}

// LeaseObservation describes a DHCP lease.
type LeaseObservation struct {
	MAC       string
	Address   string
	Hostname  string
	ClientID  string
	ExpiresAt time.Time
	Interface string
}

// LinkObservation asserts a relation between two nodes.
//
// Links are the raw material of the topology engine. The engine decides which
// links become edges, what confidence they get and whether they conflict with
// user confirmed relations.
type LinkObservation struct {
	// LocalMAC and RemoteMAC identify the two ends when known.
	LocalMAC  string
	RemoteMAC string
	// LocalIP and RemoteIP are optional hints.
	LocalIP  string
	RemoteIP string
	// Interface is the local interface involved (port, bridge, ...).
	Interface string
	// Port is the physical port, when Interface is a bridge.
	Port string
	// SpeedMbps is the negotiated link speed, when known.
	SpeedMbps int
	// LinkType classifies the relation.
	LinkType model.EdgeType
	// Confidence is the collector's own confidence in the relation.
	Confidence float64
	// Extra carries collector specific attributes.
	Extra map[string]string
}

// Capability names.
const (
	CapInterfaces = "interfaces"
	CapIPv4       = "ipv4"
	CapIPv6       = "ipv6"
	CapMAC        = "mac"
	CapHostname   = "hostname"
	CapLinks      = "links"
	CapVendor     = "vendor"
	CapSignal     = "signal"
	CapSSID       = "ssid"
)

// Confidence values by evidence class.
//
// These are the base confidences used when a collector does not supply its own.
// They are deliberately coarse: the topology engine combines multiple evidence
// records before deciding the final confidence of an edge.
const (
	// ConfidenceLLDP: an observed neighbour advertisement from the peer.
	ConfidenceLLDP = 0.95
	// ConfidenceDHCP: the DHCP server assigned the address to the MAC.
	ConfidenceDHCP = 0.85
	// ConfidenceNeighbor: an active ARP/NDP exchange.
	ConfidenceNeighbor = 0.80
	// ConfidenceFDB: the bridge learned the MAC on this port.
	ConfidenceFDB = 0.70
	// ConfidenceMDNS: an mDNS announcement.
	ConfidenceMDNS = 0.60
	// ConfidenceSubnet: both endpoints share a subnet.
	ConfidenceSubnet = 0.30
	// ConfidenceManual: the user asserted the relation.
	ConfidenceManual = 1.0
)
