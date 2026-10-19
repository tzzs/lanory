package demo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Provider adapts the demo loader to the API layer's DemoProvider interface.
//
// The API package declares:
//
//	type DemoProvider interface {
//	    Enabled() bool
//	    Seed(ctx context.Context, name string) error
//	}
//
// Go interfaces are structural, so *Provider satisfies it without this package
// importing internal/api (which would create an import cycle through the
// providers). The wiring is one line in cmd/lanory:
//
//	app.SetDemo(demo.NewProvider(store, cfg.Demo.Enabled, cfg.Demo.Seed))
//
// Provider is safe for concurrent use: Seed loads once and reloads only when
// asked to.
type Provider struct {
	store   *database.Store
	enabled bool
	seed    string

	mu     sync.Mutex
	loaded bool
	last   *Demo
}

// NewProvider returns a Provider for store.
//
// enabled reflects the configured demo mode; seed is the configured dataset
// (empty means DefaultSeed). Even when enabled is false, Seed can be called
// explicitly, which is what the UI's "load sample data" action needs.
func NewProvider(store *database.Store, enabled bool, seed string) *Provider {
	return &Provider{store: store, enabled: enabled, seed: seed}
}

// Enabled reports whether Demo Mode is configured.
func (p *Provider) Enabled() bool { return p.enabled }

// Seed loads the named dataset.
//
// An empty name uses the configured seed, then DefaultSeed. Passing an unknown
// name is an error rather than a silent no-op, so a typo in a configuration file
// is visible instead of producing an empty UI.
func (p *Provider) Seed(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	seed := name
	if seed == "" {
		seed = p.seed
	}
	if seed == "" {
		seed = DefaultSeed
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	loaded, err := Load(p.store, Options{Seed: seed, Reseed: p.loaded})
	if err != nil {
		return fmt.Errorf("demo: seed %q: %w", seed, err)
	}
	p.loaded = true
	p.last = loaded
	return nil
}

// Last returns the dataset from the most recent successful Seed, or nil.
func (p *Provider) Last() *Demo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// LoadedAt returns when the dataset was last loaded, and whether it ever was.
func (p *Provider) LoadedAt() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		return time.Time{}, false
	}
	return p.last.LoadedAt, true
}

// Active reports whether the database currently holds demo data, regardless of
// the configured flag. The API uses this for the demo badge, so a database
// seeded by a previous run is still labelled.
func (p *Provider) Active() bool { return IsDemo(p.store) }

// Sources returns the discovery sources the dataset writes, for the UI legend.
func (p *Provider) Sources() []model.DiscoverySource {
	return []model.DiscoverySource{model.SourceDemo}
}
