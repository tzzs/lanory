package database

import (
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// The zero-MAC phantom devices created by the old ARP collector must be hidden,
// never deleted, and never touch a device the user cares about.
//
// The migration is applied by Open(), so this test applies the same SQL to a
// store whose rows were inserted afterwards. That keeps the assertion on the
// statement itself rather than on the migration bookkeeping.
func TestCleanupZeroMACPhantomsHidesOnlyGeneratedNames(t *testing.T) {
	s := newTestStore(t)

	rows := []*model.Device{
		{ID: "dev-phantom", Name: "Unknown 000000"},
		{ID: "dev-phantom-mixed-case", Name: "unknown 000000 "},
		{ID: "dev-manual", Name: "Unknown 000000", IsManual: true},
		{ID: "dev-locked", Name: "Unknown 000000", IsLocked: true},
		{ID: "dev-other-unknown", Name: "Unknown 192.168.1.20"},
		{ID: "dev-real", Name: "living-room-tv"},
	}
	for _, d := range rows {
		if err := s.UpsertDevice(d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/0004_cleanup_zero_mac_phantoms.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := s.DB().Exec(string(sqlBytes)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	hidden := map[string]bool{}
	for _, id := range []string{"dev-phantom", "dev-phantom-mixed-case", "dev-manual", "dev-locked", "dev-other-unknown", "dev-real"} {
		dev, err := s.GetDevice(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		hidden[id] = dev.IsHidden
	}

	for _, id := range []string{"dev-phantom", "dev-phantom-mixed-case"} {
		if !hidden[id] {
			t.Errorf("%s should be hidden", id)
		}
	}
	for _, id := range []string{"dev-manual", "dev-locked", "dev-other-unknown", "dev-real"} {
		if hidden[id] {
			t.Errorf("%s must not be hidden", id)
		}
	}
}
