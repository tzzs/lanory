package device

import (
	"testing"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// A device nobody can name must still get a handle the user can act on. An
// address is that handle; six hex characters from the middle of a MAC are not.
func TestDisplayName(t *testing.T) {
	tests := []struct {
		name string
		ing  Ingest
		want string
	}{
		{
			name: "hostname wins",
			ing:  Ingest{Hostname: "living-room-tv", MAC: "aa:bb:cc:dd:ee:ff", Addresses: []discovery.AddressObservation{{Address: "192.168.1.20"}}},
			want: "living-room-tv",
		},
		{
			name: "vendor and mac beat an address",
			ing:  Ingest{Vendor: "Xiaomi", MAC: "aa:bb:cc:dd:ee:ff", Addresses: []discovery.AddressObservation{{Address: "192.168.1.20"}}},
			want: "Xiaomi dd:ee:ff",
		},
		{
			name: "address beats the mac suffix",
			ing:  Ingest{MAC: "04:11:22:33:44:24", Addresses: []discovery.AddressObservation{{Address: "192.168.100.55"}}},
			want: "Unknown 192.168.100.55",
		},
		{
			name: "a global ipv6 is still an address",
			ing:  Ingest{MAC: "04:11:22:33:44:24", Addresses: []discovery.AddressObservation{{Address: "2001:db8::1"}}},
			want: "Unknown 2001:db8::1",
		},
		{
			name: "link-local ipv6 is not a handle, fall back to the mac",
			ing:  Ingest{MAC: "b4:11:22:33:44:13", Addresses: []discovery.AddressObservation{{Address: "fe80::1a2b:3c4d:5e6f:7012"}}},
			want: "Unknown 33:44:13",
		},
		{
			name: "nothing known",
			ing:  Ingest{},
			want: "Unknown device",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayName(tc.ing); got != tc.want {
				t.Fatalf("displayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A generated name may be replaced by a better one, but never by a worse one:
// NDP and the bridge FDB report no address, and letting them overwrite the
// address ARP supplied would make the name flap between collectors.
func TestPlaceholderRank(t *testing.T) {
	ordered := []string{"Unknown device", "Unknown 04:67:61", "Unknown 192.168.100.55", "Xiaomi dd:ee:ff"}
	for i := 1; i < len(ordered); i++ {
		prev, cur := placeholderRank(ordered[i-1]), placeholderRank(ordered[i])
		if cur <= prev {
			t.Fatalf("placeholderRank(%q) = %d, want > placeholderRank(%q) = %d", ordered[i], cur, ordered[i-1], prev)
		}
	}
	if placeholderRank("living-room-tv") != placeholderRank("Xiaomi dd:ee:ff") {
		t.Fatal("a real name must not be outranked by a vendor placeholder")
	}
	// Names written before shortMAC() grouped its octets must still rank as
	// MAC placeholders, or they could never be upgraded.
	if placeholderRank("Unknown ddeeff") != placeholderRank("Unknown dd:ee:ff") {
		t.Fatal("an ungrouped mac placeholder must rank like a grouped one")
	}
}

func TestIsDerivedName(t *testing.T) {
	tests := []struct {
		name string
		dev  model.Device
		want bool
	}{
		{name: "empty", dev: model.Device{}, want: true},
		{name: "unknown device", dev: model.Device{Name: "Unknown device"}, want: true},
		{name: "mac placeholder", dev: model.Device{Name: "Unknown 04:67:61"}, want: true},
		{name: "address placeholder", dev: model.Device{Name: "Unknown 192.168.1.20"}, want: true},
		{name: "previous hostname", dev: model.Device{Name: "tv", Hostname: "tv"}, want: true},
		{name: "vendor placeholder", dev: model.Device{Name: "Xiaomi dd:ee:ff", Manufacturer: "Xiaomi"}, want: true},
		{name: "typed by the user", dev: model.Device{Name: "客厅电视", Hostname: "tv"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDerivedName(&tc.dev); got != tc.want {
				t.Fatalf("isDerivedName(%q) = %v, want %v", tc.dev.Name, got, tc.want)
			}
		})
	}
}
