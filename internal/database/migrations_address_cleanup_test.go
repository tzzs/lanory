package database

import (
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// The old ARP collector wrote every unresolved neighbour under one shared
// zero-MAC bucket, so a single device ended up carrying hundreds of addresses.
//
// The evidence table is what makes the difference between a ghost address and
// a real one: the raw observation records the lladdr the kernel reported.
func TestCleanupZeroMACAddressesRemovesGhostsAndKeepsRealOnes(t *testing.T) {
	s := newTestStore(t)

	dev := &model.Device{ID: "dev-mixed", Name: "Mac"}
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatal(err)
	}

	// Two addresses were only ever seen with the unresolved lladdr.
	for _, addr := range []string{"192.168.1.10", "192.168.1.11"} {
		if err := s.UpsertAddress(&model.DeviceAddress{
			DeviceID: dev.ID,
			Address:  addr,
			Family:   model.AddressFamilyIPv4,
			Source:   model.SourceARP,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveEvidence(&model.DiscoveryEvidence{
			Collector:    "arp",
			SubjectType:  "device",
			SubjectID:    dev.ID,
			EvidenceType: "neighbor",
			RawData:      addr + " dev eth3 lladdr 00:00:00:00:00:00 failed",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One address was resolved properly and must survive.
	if err := s.UpsertAddress(&model.DeviceAddress{
		DeviceID: dev.ID,
		Address:  "192.168.100.50",
		Family:   model.AddressFamilyIPv4,
		Source:   model.SourceARP,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEvidence(&model.DiscoveryEvidence{
		Collector:    "arp",
		SubjectType:  "device",
		SubjectID:    dev.ID,
		EvidenceType: "neighbor",
		RawData:      "192.168.100.50 dev br-lan lladdr b0:82:e2:61:3e:b7 reachable",
	}); err != nil {
		t.Fatal(err)
	}
	// A DHCP lease is real evidence too.
	if err := s.UpsertAddress(&model.DeviceAddress{
		DeviceID: dev.ID,
		Address:  "192.168.100.51",
		Family:   model.AddressFamilyIPv4,
		Source:   model.SourceDHCP,
	}); err != nil {
		t.Fatal(err)
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/0005_cleanup_zero_mac_addresses.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := s.DB().Exec(string(sqlBytes)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	kept := map[string]bool{}
	rows, err := s.DB().Query(`SELECT address FROM device_addresses WHERE device_id = ?`, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			t.Fatal(err)
		}
		kept[addr] = true
	}

	if kept["192.168.1.10"] || kept["192.168.1.11"] {
		t.Fatalf("ghost addresses survived: %+v", kept)
	}
	if !kept["192.168.100.50"] {
		t.Fatalf("a resolved address was removed: %+v", kept)
	}
	if !kept["192.168.100.51"] {
		t.Fatalf("a DHCP address was removed: %+v", kept)
	}
}
