// Package registry wires the shipped router adapters into one registry.
//
// It lives in its own package because internal/adapters cannot import its own
// subpackages: adapters/generic and adapters/xiaomi both import adapters, so the
// dependency only points one way. A caller that wants "every adapter Lanory
// ships" imports this package; a caller that only needs the interfaces imports
// internal/adapters.
package registry

import (
	"github.com/lanory/lanory/internal/adapters"
	"github.com/lanory/lanory/internal/adapters/generic"
	"github.com/lanory/lanory/internal/adapters/xiaomi"
)

// New returns a registry with every adapter Lanory ships.
//
// Phase 1 is the generic adapter; phase 2 adds Xiaomi. Registering a vendor here
// does not change the behaviour of the others: each adapter is probed
// independently and an empty result is never an error.
func New() *adapters.Registry {
	r := adapters.NewRegistry()
	r.Register(generic.Name, generic.New)
	r.Register(xiaomi.Name, xiaomi.New)
	return r
}

// Vendors lists the adapter vendors this build supports.
func Vendors() []string { return New().Vendors() }
