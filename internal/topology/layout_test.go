package topology

import (
	"math"
	"testing"
)

// buildLayoutFixture builds a small but realistic graph:
//
//	net-br-lan
//	  dev-router
//	    dev-win
//	    seg-br-lan-eth1
//	      dev-iphone
//	      dev-ipad
func buildLayoutFixture() *Graph {
	nodes := []Node{
		{ID: "net-br-lan", Kind: NodeKindNetwork},
		{ID: "dev-router", Kind: NodeKindDevice, Parent: "net-br-lan"},
		{ID: "dev-win", Kind: NodeKindDevice, Parent: "dev-router"},
		{ID: "seg-br-lan-eth1", Kind: NodeKindSegment, Parent: "dev-router", Unresolved: true},
		{ID: "dev-iphone", Kind: NodeKindDevice, Parent: "seg-br-lan-eth1"},
		{ID: "dev-ipad", Kind: NodeKindDevice, Parent: "seg-br-lan-eth1"},
	}
	g := &Graph{Nodes: nodes}
	PopulateChildren(g)
	return g
}

func layoutMap(ps []Placement) map[string]Placement {
	out := make(map[string]Placement, len(ps))
	for _, p := range ps {
		out[p.NodeID] = p
	}
	return out
}

// Every node must get a position, whatever the algorithm.
func TestAutoLayoutCoversEveryNode(t *testing.T) {
	g := buildLayoutFixture()
	for _, algo := range []LayoutAlgorithm{LayoutTree, LayoutLayered, LayoutForce} {
		ps := AutoLayout(g, nil, LayoutOptions{Algorithm: algo, Width: 2000, Height: 2000})
		if len(ps) != len(g.Nodes) {
			t.Errorf("%s: %d placements for %d nodes", algo, len(ps), len(g.Nodes))
			continue
		}
		seen := map[string]bool{}
		for _, p := range ps {
			if p.NodeID == "" {
				t.Errorf("%s: placement without a node id", algo)
			}
			if seen[p.NodeID] {
				t.Errorf("%s: duplicate placement for %s", algo, p.NodeID)
			}
			seen[p.NodeID] = true
		}
	}
}

// The same graph must produce the same coordinates every time. A layout that
// shuffles between runs is unusable and unpersistable.
func TestAutoLayoutIsDeterministic(t *testing.T) {
	g := buildLayoutFixture()
	for _, algo := range []LayoutAlgorithm{LayoutTree, LayoutLayered, LayoutForce} {
		first := AutoLayout(g, nil, LayoutOptions{Algorithm: algo, Width: 2000, Height: 2000})
		for i := 0; i < 5; i++ {
			again := AutoLayout(g, nil, LayoutOptions{Algorithm: algo, Width: 2000, Height: 2000})
			if len(first) != len(again) {
				t.Fatalf("%s: placement count changed between runs", algo)
			}
			for j := range first {
				if first[j] != again[j] {
					t.Fatalf("%s: placement %d changed between runs: %+v vs %+v",
						algo, j, first[j], again[j])
				}
			}
		}
	}
}

// A pinned node never moves, whatever the algorithm and whatever it wants.
func TestAutoLayoutPreservesPinned(t *testing.T) {
	g := buildLayoutFixture()
	pinned := []Placement{
		{NodeID: "dev-router", X: 42, Y: 99, Pinned: true},
		{NodeID: "dev-iphone", X: 1234.5, Y: 777, Pinned: true},
	}
	for _, algo := range []LayoutAlgorithm{LayoutTree, LayoutLayered, LayoutForce} {
		ps := layoutMap(AutoLayout(g, pinned, LayoutOptions{
			Algorithm:      algo,
			PreserveManual: true,
			Width:          2000,
			Height:         2000,
		}))
		for _, want := range pinned {
			got, ok := ps[want.NodeID]
			if !ok {
				t.Fatalf("%s: %s missing", algo, want.NodeID)
			}
			if got.X != want.X || got.Y != want.Y {
				t.Errorf("%s: pinned %s moved from (%v,%v) to (%v,%v)",
					algo, want.NodeID, want.X, want.Y, got.X, got.Y)
			}
			if !got.Pinned {
				t.Errorf("%s: %s lost its pinned flag", algo, want.NodeID)
			}
		}
	}
}

// A node the user dragged (positioned but not pinned) keeps its place when
// manual positions are preserved, and is recomputed when they are not.
func TestAutoLayoutPreservesManualPositions(t *testing.T) {
	g := buildLayoutFixture()
	existing := []Placement{{NodeID: "dev-win", X: 555, Y: 666}}

	preserved := layoutMap(AutoLayout(g, existing, LayoutOptions{
		Algorithm:      LayoutTree,
		PreserveManual: true,
		Width:          2000,
		Height:         2000,
	}))
	if got := preserved["dev-win"]; got.X != 555 || got.Y != 666 {
		t.Errorf("manual position not preserved: (%v,%v)", got.X, got.Y)
	}

	recomputed := layoutMap(AutoLayout(g, existing, LayoutOptions{
		Algorithm:      LayoutTree,
		PreserveManual: false,
		Width:          2000,
		Height:         2000,
	}))
	if got := recomputed["dev-win"]; got.X == 555 && got.Y == 666 {
		t.Error("manual position must be discarded when preserve is off")
	}
}

// A persisted position at the origin is the "never moved" sentinel and must be
// treated as absent, so new nodes do not pile up at (0,0).
func TestAutoLayoutIgnoresOriginSentinel(t *testing.T) {
	g := buildLayoutFixture()
	existing := []Placement{{NodeID: "dev-win", X: 0, Y: 0}}
	ps := layoutMap(AutoLayout(g, existing, LayoutOptions{
		Algorithm:      LayoutTree,
		PreserveManual: true,
		Width:          2000,
		Height:         2000,
	}))
	got := ps["dev-win"]
	if got.X == 0 && got.Y == 0 {
		t.Error("a node with only the origin sentinel was not laid out")
	}
}

// The tree layout must put parents above their children.
func TestTreeLayoutRespectsHierarchy(t *testing.T) {
	g := buildLayoutFixture()
	ps := layoutMap(AutoLayout(g, nil, LayoutOptions{Algorithm: LayoutTree, Width: 2000, Height: 2000}))
	for _, n := range g.Nodes {
		if n.Parent == "" {
			continue
		}
		parent, ok := ps[n.Parent]
		if !ok {
			continue
		}
		child := ps[n.ID]
		if child.Y <= parent.Y {
			t.Errorf("%s (y=%v) must be below its parent %s (y=%v)", n.ID, child.Y, n.Parent, parent.Y)
		}
	}
}

// Nodes must not overlap: the whole point of a layout.
func TestLayoutHasNoOverlap(t *testing.T) {
	g := buildLayoutFixture()
	for _, algo := range []LayoutAlgorithm{LayoutTree, LayoutLayered} {
		ps := AutoLayout(g, nil, LayoutOptions{Algorithm: algo, Width: 2000, Height: 2000})
		for i := 0; i < len(ps); i++ {
			for j := i + 1; j < len(ps); j++ {
				a, b := ps[i], ps[j]
				if a.X == b.X && a.Y == b.Y {
					t.Errorf("%s: %s and %s overlap at (%v,%v)", algo, a.NodeID, b.NodeID, a.X, a.Y)
				}
			}
		}
	}
}

// The force layout must produce finite, non-negative coordinates: a NaN or an
// infinity in the payload breaks the canvas.
func TestForceLayoutProducesFiniteCoordinates(t *testing.T) {
	g := buildLayoutFixture()
	ps := AutoLayout(g, nil, LayoutOptions{Algorithm: LayoutForce, Width: 2000, Height: 2000})
	for _, p := range ps {
		if math.IsNaN(p.X) || math.IsInf(p.X, 0) || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
			t.Fatalf("%s has a non-finite position: (%v,%v)", p.NodeID, p.X, p.Y)
		}
		if p.X < 0 || p.Y < 0 {
			t.Errorf("%s has a negative position: (%v,%v)", p.NodeID, p.X, p.Y)
		}
	}
}

func TestAutoLayoutEmptyGraph(t *testing.T) {
	ps := AutoLayout(&Graph{}, nil, DefaultLayoutOptions())
	if len(ps) != 0 {
		t.Fatalf("expected no placements, got %d", len(ps))
	}
	if ps := AutoLayout(nil, nil, DefaultLayoutOptions()); ps != nil {
		t.Fatalf("a nil graph must yield nil, got %v", ps)
	}
}

// An orphan (a node whose parent does not exist) must still be placed.
func TestAutoLayoutOrphanNode(t *testing.T) {
	g := &Graph{Nodes: []Node{
		{ID: "dev-a", Kind: NodeKindDevice, Parent: "dev-missing"},
	}}
	for _, algo := range []LayoutAlgorithm{LayoutTree, LayoutLayered, LayoutForce} {
		ps := AutoLayout(g, nil, LayoutOptions{Algorithm: algo, Width: 1000, Height: 1000})
		if len(ps) != 1 {
			t.Fatalf("%s: orphan dropped", algo)
		}
	}
}

func TestParseAlgorithm(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want LayoutAlgorithm
	}{
		{"tree", LayoutTree},
		{"layered", LayoutLayered},
		{"force", LayoutForce},
		{"", LayoutTree},
		{"nonsense", LayoutTree},
	} {
		if got := ParseAlgorithm(tc.in); got != tc.want {
			t.Errorf("ParseAlgorithm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultLayoutOptions(t *testing.T) {
	o := DefaultLayoutOptions()
	if o.Algorithm != LayoutTree {
		t.Errorf("algorithm = %q", o.Algorithm)
	}
	if !o.PreserveManual {
		t.Error("manual positions must be preserved by default")
	}
	if o.Width <= 0 || o.Height <= 0 {
		t.Error("the viewport must be positive")
	}
}
