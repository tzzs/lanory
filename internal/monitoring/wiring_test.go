package monitoring

import (
	"testing"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/device"
	"github.com/lanory/lanory/internal/events"
)

// TestWiringCompiles documents the exact wiring the main agent performs: the real
// store, the real device identity engine, the real event bus and the real
// layered config. It fails to compile if any of those no longer satisfies the
// narrow interfaces this package declares.
func TestWiringCompiles(t *testing.T) {
	s := testStore(t)
	bus := events.New(64)
	devEngine := device.New(s, bus)
	cfg := config.Default()

	// The store, the bus and the device engine must satisfy the narrow
	// interfaces declared here.
	var _ Store = s
	var _ Publisher = bus
	var _ DeviceStateSetter = devEngine

	eng := NewEngine(s, bus, NewExecutor(DefaultExecutorConfig()), devEngine, EngineConfig{
		Monitoring: cfg.Monitoring,
		Database:   cfg.Database,
	})

	// The engine must expose everything the HTTP layer needs.
	if eng.Running() {
		t.Fatal("engine reports running before Start")
	}
	var _ *StateMachine = eng.StateMachine()
	_, _ = eng.LinkStatus()
	_ = eng.CheckInternet(t.Context())
	_, _ = eng.Status(50)
	_ = eng.Prune(t.Context())
	_ = eng.Reload(t.Context())
}
