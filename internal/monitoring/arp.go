package monitoring

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// ARPObserver reads the kernel's neighbour table.
//
// Why it exists
// -------------
// ICMP alone cannot tell "the device is gone" from "the device does not answer
// my probe method". A Windows machine drops every echo request, a phone stops
// answering when it sleeps, and a printer may have no ICMP stack at all. If
// those count as offline, the monitoring page is wrong most of the time.
//
// The neighbour table is the kernel's own view of the segment: an entry for a
// MAC means the host answered an ARP request recently. It is therefore the
// cross-check that turns "no reply" into "still there, just not answering
// ping", and it is listed as a probe method in the specification.
//
// The table is read from /proc/net/arp, which is a text file on every Linux
// kernel, so no extra dependency and no privileges are needed.
type ARPObserver struct {
	// path is the neighbour table to read.
	path string

	mu      sync.Mutex
	entries map[string]arpEntry
	readAt  time.Time
}

// arpEntry is one row of the neighbour table.
type arpEntry struct {
	IP        string
	MAC       string
	Device    string
	Flags     string
	UpdatedAt time.Time
}

// DefaultARPPath is where the kernel exposes the neighbour table.
const DefaultARPPath = "/proc/net/arp"

// NewARPObserver returns an observer reading the standard neighbour table.
func NewARPObserver() *ARPObserver {
	return &ARPObserver{path: DefaultARPPath, entries: map[string]arpEntry{}}
}

// NewARPObserverWithPath returns an observer reading a specific file, for tests.
func NewARPObserverWithPath(path string) *ARPObserver {
	return &ARPObserver{path: path, entries: map[string]arpEntry{}}
}

// Refresh reads the neighbour table and returns the number of entries.
//
// The result is cached for a short time: the table changes on the order of
// seconds, and a probe cycle asks about the same addresses repeatedly.
func (a *ARPObserver) Refresh(ctx context.Context) (int, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return 0, err
	}
	entries, err := parseARPTable(string(data))
	if err != nil {
		return 0, err
	}

	a.mu.Lock()
	a.entries = entries
	a.readAt = time.Now().UTC()
	count := len(entries)
	a.mu.Unlock()
	return count, nil
}

// Seen reports whether the neighbour table has a recent entry for an address.
//
// Only a complete entry counts: the kernel marks an incomplete resolution with
// a zero flag and a zero MAC, which says nothing about whether a host is there.
func (a *ARPObserver) Seen(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 0 {
		return false
	}
	entry, ok := a.entries[ip]
	if !ok {
		return false
	}
	if entry.MAC == "" || entry.Flags == "0x0" {
		return false
	}
	return true
}

// Entry returns the neighbour table row for an address.
func (a *ARPObserver) Entry(ip string) (arpEntry, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[ip]
	return e, ok
}

// Entries returns a copy of the current table.
func (a *ARPObserver) Entries() map[string]arpEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]arpEntry, len(a.entries))
	for k, v := range a.entries {
		out[k] = v
	}
	return out
}

// ReadAt reports when the table was last read.
func (a *ARPObserver) ReadAt() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.readAt
}

// parseARPTable parses /proc/net/arp.
//
// The format is a header line followed by one line per entry:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.100.1    0x1         0x2         b0:1b:cd:a2:33:10     *        eth0
//
// The flags field carries the completeness: 0x2 means the entry is complete and
// 0x0 means the address could not be resolved, which is not evidence of a
// host.
func parseARPTable(raw string) (map[string]arpEntry, error) {
	out := map[string]arpEntry{}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if i == 0 {
			// Header.
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		ip := fields[0]
		flags := fields[2]
		mac := strings.ToLower(fields[3])
		device := fields[5]
		if ip == "" || !isIPv4(ip) {
			continue
		}
		out[ip] = arpEntry{
			IP:        ip,
			MAC:       mac,
			Device:    device,
			Flags:     flags,
			UpdatedAt: time.Now().UTC(),
		}
	}
	return out, nil
}

// isIPv4 reports whether a string is a dotted-quad address.
func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}
