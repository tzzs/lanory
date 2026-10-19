// Package mdns implements the mDNS / Bonjour collector.
//
// mDNS is the lowest-priority source: it is easy to spoof, it only sees devices
// that chose to announce themselves, and it reports names rather than topology.
// But it is often the ONLY source that knows a device's hostname, and a hostname
// is what the user actually recognises in the UI.
//
// The collector reads the output of `avahi-browse` (the standard mDNS browser
// on Linux, available as a package on OpenWrt) or `dns-sd` on macOS. When
// neither is present it reports unsupported.
//
// What it produces:
//
//   - hostnames and service types, which the identity engine uses to name and
//     classify devices
//   - address records, when the tool prints them
//   - a link observation with the lowest confidence, because an mDNS
//     announcement says nothing about the physical path
package mdns

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Command names, in preference order.
const (
	cmdAvahi = "avahi-browse"
	cmdDNSSD = "dns-sd"
)

// commandTimeout bounds one avahi-browse invocation.
const commandTimeout = 6 * time.Second

// Collector reads mDNS announcements.
type Collector struct {
	*discovery.BaseCollector

	// mdnsPath overrides the command output with a fixture file.
	mdnsPath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns an mDNS collector using the live system command.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorMDNS,
		"mDNS/Bonjour announcements: hostnames and service types",
		false, // support is probed in Supported()
		discovery.CapHostname, discovery.CapIPv4, discovery.CapIPv6, discovery.CapVendor,
	)
	return c
}

// NewWithPath builds a collector that reads its mDNS dump from a file. It is
// the seam the fixture tests use.
func NewWithPath(path string) *Collector {
	c := New()
	c.mdnsPath = path
	c.SetSupported(true)
	return c
}

// Supported reports whether an mDNS browser is available.
func (c *Collector) Supported() bool {
	if c.mdnsPath != "" {
		return true
	}
	return discovery.LookPath(cmdAvahi) || discovery.LookPath(cmdDNSSD)
}

// Sources reports which input the collector will use.
func (c *Collector) Sources() []string {
	if c.mdnsPath != "" {
		return []string{"file:" + c.mdnsPath}
	}
	if discovery.LookPath(cmdAvahi) {
		return []string{"avahi-browse -a -t -r -p"}
	}
	return []string{"dns-sd -B _services._dns-sd._udp"}
}

// Collect reads the mDNS announcements and returns one generic observation per
// resolved service.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}

	data, err := c.readMDNS(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	var out []discovery.Result
	for _, rec := range ParseAvahiBrowse(data) {
		r := discovery.NewResult(discovery.ResultGeneric, model.SourceMDNS, now)
		r.Attributes = map[string]string{
			"hostname": rec.Hostname,
			"service":  rec.Service,
			"address":  rec.Address,
			"port":     itoa(rec.Port),
			"txt":      rec.TXT,
		}
		if rec.MAC != "" {
			r.Attributes["mac"] = rec.MAC
		}
		out = append(out, *r)

		// An address observation, so the identity engine can attach the
		// hostname to the device that owns the address.
		if rec.Address != "" {
			family := model.AddressFamilyIPv4
			if strings.Contains(rec.Address, ":") {
				family = model.AddressFamilyIPv6
			}
			out = append(out, *withAddress(discovery.NewResult(discovery.ResultAddress, model.SourceMDNS, now),
				&discovery.AddressObservation{
					Address:  rec.Address,
					Family:   family,
					MAC:      rec.MAC,
					Hostname: rec.Hostname,
				}))
		}

		// The lowest-confidence link: an announcement proves nothing about the
		// physical path, only that the device is on the same broadcast domain.
		if rec.MAC != "" || rec.Address != "" {
			out = append(out, *withLink(discovery.NewResult(discovery.ResultLink, model.SourceMDNS, now),
				&discovery.LinkObservation{
					RemoteMAC:  rec.MAC,
					RemoteIP:   rec.Address,
					LinkType:   model.EdgeTypeLogicalNetwork,
					Confidence: discovery.ConfidenceMDNS,
					Extra: map[string]string{
						"hostname": rec.Hostname,
						"service":  rec.Service,
					},
				}))
		}
	}
	return out, nil
}

// readMDNS returns the raw mDNS browser output.
func (c *Collector) readMDNS(ctx context.Context) ([]byte, error) {
	if c.mdnsPath != "" {
		data, _, err := discovery.ReadFileCapped(c.mdnsPath, 0)
		if err != nil {
			return nil, fmt.Errorf("mdns: read %s: %w", c.mdnsPath, err)
		}
		return data, nil
	}
	if discovery.LookPath(cmdAvahi) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout},
			cmdAvahi, "-a", "-t", "-r", "-p")
		if err == nil {
			return out, nil
		}
		if strings.Contains(err.Error(), discovery.ErrNotSupported.Error()) {
			c.SetSupported(false)
			return nil, err
		}
	}
	if discovery.LookPath(cmdDNSSD) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout},
			cmdDNSSD, "-B", "_services._dns-sd._udp")
		if err == nil {
			return out, nil
		}
	}
	return nil, nil //nolint:nilerr // no mDNS browser is a supported-false state
}

// ---------------------------------------------------------------------------
// parsing
// ---------------------------------------------------------------------------

// Record is one resolved mDNS service.
type Record struct {
	Hostname string
	Service  string
	Address  string
	Port     int
	MAC      string
	TXT      string
}

// ParseAvahiBrowse parses the parseable (`-p`) output of `avahi-browse -a -t -r`.
//
// The format is line oriented with a leading marker:
//
//	=;eth0;IPv4;hostname;_http._tcp;local
//	hostname;192.0.2.151;80
//	 txt key=value
//
// The first line is the service discovery record; the following lines are the
// resolution record and its TXT entries. avahi-browse -r prints both, so the
// parser keeps the address and port from the resolution line.
func ParseAvahiBrowse(data []byte) []Record {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var (
		out   []Record
		index = map[string]int{} // hostname+service -> position in out
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "="):
			rec, ok := parseAvahiService(line)
			if !ok {
				continue
			}
			key := rec.Hostname + "|" + rec.Service
			if _, exists := index[key]; !exists {
				out = append(out, rec)
				index[key] = len(out) - 1
			}
		case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
			// Discovery / removal markers carry no address information.
		default:
			// Resolution line: "hostname;address;port". The hostname here
			// carries the ".local" suffix that the service line does not, so
			// it is normalised before matching.
			if fields := strings.Split(line, ";"); len(fields) >= 2 {
				host := strings.TrimSuffix(strings.TrimSpace(fields[0]), ".local")
				addr := strings.TrimSpace(fields[1])
				if host == "" || addr == "" {
					continue
				}
				// Attach to the most recent record for that hostname.
				for i := len(out) - 1; i >= 0; i-- {
					if out[i].Hostname == host {
						out[i].Address = addr
						if len(fields) >= 3 {
							out[i].Port = atoi(fields[2])
						}
						break
					}
				}
			}
		}
	}
	return out
}

// parseAvahiService parses a "=;iface;proto;hostname;service;domain" line.
func parseAvahiService(line string) (Record, bool) {
	parts := strings.Split(line, ";")
	if len(parts) < 5 {
		return Record{}, false
	}
	host := strings.TrimSpace(parts[3])
	if host == "" {
		return Record{}, false
	}
	service := strings.TrimSpace(parts[4])
	// A hostname may be "<name>.local"; strip the mDNS suffix for display.
	host = strings.TrimSuffix(host, ".local")
	return Record{Hostname: host, Service: service}, true
}

func atoi(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n := 0
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return n
		}
		n = n*10 + int(v[i]-'0')
	}
	return n
}

func itoa(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}

// withAddress attaches an address observation to a result and returns it.
func withAddress(r *discovery.Result, o *discovery.AddressObservation) *discovery.Result {
	r.Address = o
	return r
}

// withLink attaches a link observation to a result and returns it.
func withLink(r *discovery.Result, o *discovery.LinkObservation) *discovery.Result {
	r.Link = o
	return r
}
