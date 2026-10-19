package topology

import (
	"errors"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Service is the concrete topology engine the HTTP layer consumes.
//
// It exists so internal/api can depend on its own narrow TopologyProvider
// interface (Build / Evidence / AutoLayout) without importing this package. The
// wiring layer does:
//
//	svc := topology.NewService(store, cfg)
//	app.SetTopology(svc)
//
// Everything here is read-mostly: Build and Evidence read the store, AutoLayout
// writes positions through SaveLayout because the user explicitly asked for a
// new arrangement.
type Service struct {
	store *database.Store
	cfg   config.TopologyConfig
	// Now is the clock used for the build timestamp; overridden in tests.
	Now func() time.Time
}

// NewService returns a topology service reading through store.
func NewService(store *database.Store, cfg *config.Config) *Service {
	s := &Service{store: store, Now: func() time.Time { return time.Now().UTC() }}
	if cfg != nil {
		s.cfg = cfg.Topology
	} else {
		s.cfg = config.Default().Topology
	}
	return s
}

// Build returns the current topology graph as the API contract type.
//
// The graph is rebuilt from stored evidence on every call rather than cached:
// the device and edge tables are small (hundreds of rows at worst), the query is
// indexed, and a stale cache would mean the UI shows a network that no longer
// exists.
func (s *Service) Build() (*model.Topology, error) {
	if s.store == nil {
		return nil, errors.New("topology: service has no store")
	}
	layout, err := s.store.ListLayout()
	if err != nil {
		return nil, err
	}
	graph, err := s.builder().Build()
	if err != nil {
		return nil, err
	}
	return graph.ToModel(layout), nil
}

// builder constructs a Builder with the configured options.
func (s *Service) builder() *Builder {
	opts := Options{
		UnverifiedEdgeThreshold: s.cfg.UnverifiedEdgeThreshold,
		Now:                     s.now(),
	}
	if opts.UnverifiedEdgeThreshold <= 0 {
		opts.UnverifiedEdgeThreshold = UnverifiedThreshold
	}
	return NewBuilder(s.store).WithOptions(opts)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Evidence returns the edges with the discovery records that back them, plus
// the devices that are not connected to anything.
//
// The unmapped list is the honest answer to "why is this device floating?":
// either nothing was ever observed about where it sits, or the observation was
// too weak to place it.
func (s *Service) Evidence() (*model.TopologyEvidence, error) {
	if s.store == nil {
		return nil, errors.New("topology: service has no store")
	}
	edges, err := s.store.ListEdges(database.EdgeFilter{IncludeUserDefined: true})
	if err != nil {
		return nil, err
	}
	devices, _, err := s.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 500})
	if err != nil {
		return nil, err
	}

	connected := map[string]bool{}
	out := make([]model.EdgeEvidence, 0, len(edges))
	for _, e := range edges {
		connected[e.SourceID] = true
		connected[e.TargetID] = true
		ev, err := s.store.ListEvidence("edge", e.ID)
		if err != nil {
			return nil, err
		}
		if ev == nil {
			ev = []model.DiscoveryEvidence{}
		}
		out = append(out, model.EdgeEvidence{Edge: e, Evidence: ev})
	}

	unmapped := make([]model.DeviceEvidence, 0)
	for _, d := range devices {
		if connected[d.ID] {
			continue
		}
		ev, err := s.store.ListEvidenceForDevice(d.ID)
		if err != nil {
			return nil, err
		}
		addrs, err := s.store.ListAddresses(d.ID)
		if err != nil {
			return nil, err
		}
		if ev == nil {
			ev = []model.DiscoveryEvidence{}
		}
		if addrs == nil {
			addrs = []model.DeviceAddress{}
		}
		unmapped = append(unmapped, model.DeviceEvidence{Device: d, Evidence: ev, Addresses: addrs})
	}

	return &model.TopologyEvidence{Edges: out, Unmapped: unmapped}, nil
}

// AutoLayout recomputes every node position and persists the result.
//
// Pinned nodes never move, and (when the configuration asks for it) nodes the
// user dragged keep their saved position. The returned layout is what was
// stored, so the UI can render it without a second round trip.
func (s *Service) AutoLayout(algorithm string) ([]model.TopologyLayout, error) {
	if s.store == nil {
		return nil, errors.New("topology: service has no store")
	}
	graph, err := s.builder().Build()
	if err != nil {
		return nil, err
	}
	existing, err := s.store.ListLayout()
	if err != nil {
		return nil, err
	}

	opts := DefaultLayoutOptions()
	opts.Algorithm = ParseAlgorithm(algorithm)
	opts.PreserveManual = s.cfg.PreserveManualLayout
	if !s.cfg.AutoLayout && opts.PreserveManual {
		// Auto layout is disabled and manual positions are preserved: there is
		// nothing to recompute, so return the stored positions untouched rather
		// than silently rearranging a network the user arranged by hand.
		return existing, nil
	}

	placements := AutoLayout(graph, FromModelLayout(existing), opts)
	out := ToModelLayout(placements)
	for i := range out {
		l := &out[i]
		if err := s.store.SaveLayout(l); err != nil {
			return nil, err
		}
	}
	return out, nil
}
