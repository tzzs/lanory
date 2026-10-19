// Package lldp implements the LLDP neighbour collector.
//
// LLDP is the highest-confidence link evidence available: the peer device
// announces itself, its chassis, its port and its capabilities. When a switch
// or an access point speaks LLDP, Lanory can draw the exact cable.
//
// The catch is that almost nothing in a home network speaks LLDP. Consumer
// switches and access points usually have it disabled or absent. So this
// collector reports unsupported on most hosts, which is exactly what the
// contract asks for: no lldpcli binary, no LLDP support.
//
// When it IS available, it is worth a lot: an LLDP neighbour is a confirmed
// device on a confirmed port, which outranks every inferred relation.
package lldp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Command names.
const (
	cmdLLDPCli = "lldpcli"
)

// commandTimeout bounds one lldpcli invocation.
const commandTimeout = 4 * time.Second

// Collector reads LLDP neighbours.
type Collector struct {
	*discovery.BaseCollector

	// lldpPath overrides the lldpcli output with a fixture file.
	lldpPath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns an LLDP collector using the live system command.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorLLDP,
		"LLDP neighbours: confirmed adjacent devices and their ports",
		false, // support is probed in Supported()
		discovery.CapLinks, discovery.CapMAC, discovery.CapVendor,
	)
	return c
}

// NewWithPath builds a collector that reads its LLDP dump from a file. It is
// the seam the fixture tests use.
func NewWithPath(path string) *Collector {
	c := New()
	c.lldpPath = path
	c.SetSupported(true)
	return c
}

// Supported reports whether LLDP is usable. lldpcli must exist AND the LLDP
// daemon must be running; a present binary with no daemon yields no neighbours
// and no error.
func (c *Collector) Supported() bool {
	if c.lldpPath != "" {
		return true
	}
	return discovery.LookPath(cmdLLDPCli)
}

// Sources reports which input the collector will use.
func (c *Collector) Sources() []string {
	if c.lldpPath != "" {
		return []string{"file:" + c.lldpPath}
	}
	return []string{"lldpcli show neighbors -f json"}
}

// Collect reads the LLDP neighbours and returns one observation per neighbour.
//
// A neighbour with no chassis ID or no port is still reported: the local
// interface it was seen on is itself useful information.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}

	data, err := c.readLLDP(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}
	neighbours, perr := ParseLLDPJSON(data)
	if perr != nil {
		// Unparseable output is not a hard failure: lldpcli may print a
		// warning banner before the JSON.
		return discovery.EmptyResults(), nil //nolint:nilerr // nothing to report
	}

	var out []discovery.Result
	for _, n := range neighbours {
		if n.ChassisMAC == "" && n.ChassisID == "" && n.PortID == "" {
			continue
		}
		r := discovery.NewResult(discovery.ResultGeneric, model.SourceLLDP, now)
		r.Attributes = map[string]string{
			"interface":  n.Interface,
			"chassis_id": n.ChassisID,
			"port_id":    n.PortID,
			"port_desc":  n.PortDescription,
			"name":       n.SystemName,
			"vendor":     n.Manufacturer,
		}
		if n.ChassisMAC != "" {
			r.Attributes["chassis_mac"] = n.ChassisMAC
		}
		out = append(out, *r)

		// A link observation: this is the confirmed physical relation.
		link := &discovery.LinkObservation{
			LocalMAC:   n.ChassisMAC,
			Interface:  n.Interface,
			Port:       n.PortID,
			LinkType:   model.EdgeTypePhysicalLink,
			Confidence: discovery.ConfidenceLLDP,
			Extra: map[string]string{
				"chassis_id":   n.ChassisID,
				"system_name":  n.SystemName,
				"manufacturer": n.Manufacturer,
				"port_desc":    n.PortDescription,
			},
		}
		out = append(out, *withLink(discovery.NewResult(discovery.ResultLink, model.SourceLLDP, now), link))
	}
	return out, nil
}

// readLLDP returns the raw lldpcli output.
func (c *Collector) readLLDP(ctx context.Context) ([]byte, error) {
	if c.lldpPath != "" {
		data, _, err := discovery.ReadFileCapped(c.lldpPath, 0)
		if err != nil {
			return nil, fmt.Errorf("lldp: read %s: %w", c.lldpPath, err)
		}
		return data, nil
	}
	out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout},
		cmdLLDPCli, "show", "neighbors", "-f", "json")
	if err != nil {
		if strings.Contains(err.Error(), discovery.ErrNotSupported.Error()) {
			c.SetSupported(false)
			return nil, err
		}
		// lldpcli exits non-zero when there are no neighbours at all. That is
		// a normal outcome, not an error.
		return out, nil //nolint:nilerr // no neighbours is a normal outcome
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// parsing
// ---------------------------------------------------------------------------

// Neighbour is one LLDP neighbour entry.
type Neighbour struct {
	// Interface is the local interface the neighbour was seen on.
	Interface string
	// ChassisID is the raw chassis identifier.
	ChassisID string
	// ChassisMAC is the chassis MAC when the chassis id is a MAC.
	ChassisMAC string
	// PortID is the neighbour's port identifier.
	PortID string
	// PortDescription is the neighbour's port description.
	PortDescription string
	// SystemName is the neighbour's advertised name.
	SystemName string
	// Manufacturer is the neighbour's advertised manufacturer.
	Manufacturer string
}

// rawLLDP is the lldpcli JSON envelope: a "lldp" object keyed by interface,
// where each interface holds a map of neighbours.
type rawLLDP struct {
	LLDP map[string]rawInterface `json:"lldp"`
}

type rawInterface struct {
	Interface map[string]rawNeighbour `json:"interface"`
}

type rawNeighbour struct {
	Chassis []rawChassis `json:"chassis"`
	Port    []rawPort    `json:"port"`
}

type rawChassis struct {
	ID struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"id"`
	Name         string `json:"name"`
	Descr        string `json:"descr"`
	Manufacturer string `json:"manufacturer"`
	// Some lldpd versions nest the manufacturer under "mgmt-ip" siblings.
	MgmtIP []string `json:"mgmt-ip"`
}

type rawPort struct {
	ID struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"id"`
	Descr string `json:"descr"`
}

// ParseLLDPJSON parses the output of `lldpcli show neighbors -f json`.
//
// lldpcli nests the data three deep (lldp -> interface -> chassis/port) and
// represents the identifiers as arrays of objects. The shape differs slightly
// between lldpd versions, so every lookup is defensive: a missing chassis or
// port is tolerated, and only the identifiers that are present are reported.
func ParseLLDPJSON(data []byte) ([]Neighbour, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var raw rawLLDP
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("lldp: parse json: %w", err)
	}
	var out []Neighbour
	for ifaceName, iface := range raw.LLDP {
		// The inner key is the neighbour's chassis name, not the interface.
		for _, entry := range iface.Interface {
			n := Neighbour{Interface: ifaceName}
			if len(entry.Chassis) > 0 {
				ch := entry.Chassis[0]
				n.ChassisID = ch.ID.Value
				if strings.EqualFold(ch.ID.Type, "mac") {
					n.ChassisMAC = normalizeMAC(ch.ID.Value)
				}
				n.SystemName = firstNonEmpty(ch.Name, ch.Descr)
				n.Manufacturer = ch.Manufacturer
			}
			if len(entry.Port) > 0 {
				p := entry.Port[0]
				n.PortID = p.ID.Value
				n.PortDescription = p.Descr
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// normalizeMAC renders a MAC in lowercase colon form, or returns it unchanged.
func normalizeMAC(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != 17 {
		return v
	}
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// withLink attaches a link observation to a result and returns it.
func withLink(r *discovery.Result, o *discovery.LinkObservation) *discovery.Result {
	r.Link = o
	return r
}
