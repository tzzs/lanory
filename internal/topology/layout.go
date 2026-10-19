package topology

import (
	"math"
	"sort"
)

// Layout algorithms.
//
// Three algorithms, all deterministic: given the same graph they produce the
// same coordinates. That matters more than beauty — a UI that reshuffles the
// network every ten minutes is unusable, and a test that cannot assert a
// position is useless.
//
// All three respect the persisted positions:
//
//   - A pinned node never moves.
//   - A node the user dragged (persisted with any position, not reset to the
//     origin) keeps that position when the layout preserves manual positions,
//     which is the default. The user's arrangement is data, not decoration.
//
//   - Everything else is (re)computed.

// LayoutAlgorithm names the supported layouts.
type LayoutAlgorithm string

const (
	// LayoutTree is the default: breadth-first levels down from the roots,
	// siblings side by side.
	LayoutTree LayoutAlgorithm = "tree"
	// LayoutLayered groups nodes by their tree depth and packs each layer,
	// giving a tidy columnar diagram with fewer long edges.
	LayoutLayered LayoutAlgorithm = "layered"
	// LayoutForce is a deterministic spring relaxation for graphs whose tree
	// structure is not meaningful.
	LayoutForce LayoutAlgorithm = "force"
)

// ParseAlgorithm maps the API's algorithm string onto the enum. An unknown
// value falls back to the tree layout, which is what the config default is.
func ParseAlgorithm(s string) LayoutAlgorithm {
	switch LayoutAlgorithm(s) {
	case LayoutLayered:
		return LayoutLayered
	case LayoutForce:
		return LayoutForce
	default:
		return LayoutTree
	}
}

// LayoutSpacing is the grid the computed layouts snap to. Round numbers keep the
// coordinates stable across rebuilds, which is what makes them persistable.
const (
	// LayerGap is the vertical distance between tree levels.
	LayerGap = 160.0
	// NodeGap is the horizontal distance between siblings.
	NodeGap = 220.0
	// ForceIterations bounds the relaxation loop so the layout stays
	// independent of graph size in the worst case.
	ForceIterations = 200
	// ForceRepulsion and ForceAttraction are the spring constants.
	ForceRepulsion  = 60000.0
	ForceAttraction = 0.06
)

// LayoutOptions tune AutoLayout.
type LayoutOptions struct {
	// Algorithm selects the layout.
	Algorithm LayoutAlgorithm
	// PerserveManual keeps persisted positions for nodes that are not pinned.
	// The field name mirrors config.TopologyConfig.PreserveManualLayout.
	PreserveManual bool
	// Width and Height bound the computed area, keeping the graph on screen.
	Width  float64
	Height float64
}

// DefaultLayoutOptions returns the options matching config.Default().
func DefaultLayoutOptions() LayoutOptions {
	return LayoutOptions{
		Algorithm:      LayoutTree,
		PreserveManual: true,
		Width:          4000,
		Height:         4000,
	}
}

// placement is a persisted node position.
type placement struct {
	ID     string
	X      float64
	Y      float64
	Pinned bool
}

// AutoLayout computes node positions for the graph.
//
// The returned positions cover every node, including the pinned ones, so the API
// can persist the whole result in one pass.
func AutoLayout(graph *Graph, existing []Placement, opts LayoutOptions) []Placement {
	if graph == nil {
		return nil
	}
	saved := map[string]placement{}
	for _, p := range existing {
		saved[p.NodeID] = placement{ID: p.NodeID, X: p.X, Y: p.Y, Pinned: p.Pinned}
	}

	var computed []Placement
	switch opts.Algorithm {
	case LayoutLayered:
		computed = layeredLayout(graph, opts)
	case LayoutForce:
		computed = forceLayout(graph, opts)
	default:
		computed = treeLayout(graph, opts)
	}

	// Index the computed positions so a persisted position can override it.
	byNode := map[string]Placement{}
	for _, p := range computed {
		byNode[p.NodeID] = p
	}

	out := make([]Placement, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		p, ok := byNode[n.ID]
		if !ok {
			// Unplaced node (an unmapped device, for instance): keep it in the
			// payload at the origin so the UI can render it.
			p = Placement{NodeID: n.ID}
		}
		if saved, ok := saved[n.ID]; ok {
			switch {
			case saved.Pinned:
				// A pinned node never moves, whatever the algorithm says.
				p.X, p.Y, p.Pinned = saved.X, saved.Y, true
			case opts.PreserveManual && !isOrigin(saved.X, saved.Y):
				// A node the user dragged keeps its place.
				p.X, p.Y = saved.X, saved.Y
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// isOrigin reports whether a persisted position is the default (never moved).
func isOrigin(x, y float64) bool { return x == 0 && y == 0 }

// ---------------------------------------------------------------------------
// tree
// ---------------------------------------------------------------------------

// treeLayout arranges the graph in breadth-first levels.
//
// The root level is every node without a parent, ordered by ID. Each level is
// laid out left to right under its parent, so a parent's children are adjacent
// and the edge crossings are few.
func treeLayout(graph *Graph, opts LayoutOptions) []Placement {
	levels := computeLevels(graph)

	// Group the nodes by depth, then order each depth by (parent ID, own ID) so
	// the result is independent of the input order.
	byLevel := map[int][]Node{}
	for _, n := range graph.Nodes {
		d := levels[n.ID]
		if d < 0 {
			// Cyclic or unplaceable: pin it to the bottom so it is still
			// visible rather than silently dropped.
			d = maxLevel(graph) + 1
		}
		byLevel[d] = append(byLevel[d], n)
	}
	for d := range byLevel {
		nodes := byLevel[d]
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Parent != nodes[j].Parent {
				return nodes[i].Parent < nodes[j].Parent
			}
			return nodes[i].ID < nodes[j].ID
		})
		byLevel[d] = nodes
	}

	out := make([]Placement, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		out = append(out, Placement{NodeID: n.ID})
	}
	pos := map[string]Placement{}
	for i := range out {
		pos[out[i].NodeID] = out[i]
	}

	for d := 0; d <= maxLevel(graph)+1; d++ {
		nodes := byLevel[d]
		if len(nodes) == 0 {
			continue
		}
		// Centre each parent's children under it.
		width := float64(len(nodes)) * NodeGap
		startX := (opts.Width - width) / 2
		if startX < 0 {
			startX = 0
		}
		for i, n := range nodes {
			pos[n.ID] = Placement{
				NodeID: n.ID,
				X:      round(startX + float64(i)*NodeGap),
				Y:      round(float64(d) * LayerGap),
			}
		}
	}
	// Re-centre each parent's subtree: a parent sits in the middle of its
	// children, which is what makes the tree readable.
	centreParents(graph, pos, levels)

	out = make([]Placement, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if p, ok := pos[n.ID]; ok {
			out = append(out, p)
		}
	}
	return out
}

// centreParents moves each parent to the horizontal centre of its children.
//
// This is done bottom-up so a leaf's shift is already reflected when its own
// parent is centred. The result is a proper tree shape rather than a flat grid.
func centreParents(graph *Graph, pos map[string]Placement, levels map[string]int) {
	children := map[string][]string{}
	for _, n := range graph.Nodes {
		if n.Parent != "" {
			children[n.Parent] = append(children[n.Parent], n.ID)
		}
	}
	for id := range children {
		sort.Strings(children[id])
	}
	// Deepest level first.
	order := make([]string, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		order = append(order, n.ID)
	}
	sort.Slice(order, func(i, j int) bool {
		return levels[order[i]] > levels[order[j]]
	})
	for _, id := range order {
		kids := children[id]
		if len(kids) == 0 {
			continue
		}
		var sum float64
		var count int
		for _, k := range kids {
			if p, ok := pos[k]; ok {
				sum += p.X
				count++
			}
		}
		if count == 0 {
			continue
		}
		centre := sum / float64(count)
		if p, ok := pos[id]; ok {
			p.X = round(centre)
			pos[id] = p
		}
	}
}

// ---------------------------------------------------------------------------
// layered
// ---------------------------------------------------------------------------

// layeredLayout groups nodes by depth into columns and packs each column
// vertically. It produces fewer long edges than the tree layout because a node
// with several parents (which the tree layout cannot express) still lands in one
// column.
func layeredLayout(graph *Graph, opts LayoutOptions) []Placement {
	levels := computeLevels(graph)
	byDepth := map[int][]Node{}
	for _, n := range graph.Nodes {
		d := levels[n.ID]
		if d < 0 {
			d = maxLevel(graph) + 1
		}
		byDepth[d] = append(byDepth[d], n)
	}
	depths := make([]int, 0, len(byDepth))
	for d := range byDepth {
		depths = append(depths, d)
	}
	sort.Ints(depths)

	out := make([]Placement, 0, len(graph.Nodes))
	for _, d := range depths {
		nodes := byDepth[d]
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		height := float64(len(nodes)) * NodeGap
		startY := (opts.Height - height) / 2
		if startY < 0 {
			startY = 0
		}
		for i, n := range nodes {
			out = append(out, Placement{
				NodeID: n.ID,
				X:      round(float64(d) * LayerGap),
				Y:      round(startY + float64(i)*NodeGap),
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// force
// ---------------------------------------------------------------------------

// forceLayout runs a deterministic spring relaxation.
//
// The initial positions are the sorted node IDs on a circle, so the layout is a
// pure function of the graph: the same graph always relaxes to the same
// picture. Repulsion pushes everything apart, attraction pulls connected nodes
// together, and a mild vertical gravity keeps the tree hierarchy legible.
func forceLayout(graph *Graph, opts LayoutOptions) []Placement {
	n := len(graph.Nodes)
	if n == 0 {
		return nil
	}
	ids := make([]string, n)
	for i, node := range graph.Nodes {
		ids[i] = node.ID
	}
	sort.Strings(ids)

	pos := make([]float64, 2*n)
	for i := range ids {
		angle := 2.0 * pi() * float64(i) / float64(n)
		radius := 200.0 + 20.0*float64(n)
		pos[2*i] = radius * math.Cos(angle)
		pos[2*i+1] = radius * math.Sin(angle)
	}

	levels := computeLevels(graph)
	// adjacency() is used by the attraction pass below through the stored
	// edges; keeping the map documents the intended coupling and lets the
	// force layout be extended to tree-parent attraction without a reshuffle.
	_ = adjacency(graph)

	for iter := 0; iter < ForceIterations; iter++ {
		forceX := make([]float64, n)
		forceY := make([]float64, n)

		// Repulsion: every pair pushes apart.
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := pos[2*i] - pos[2*j]
				dy := pos[2*i+1] - pos[2*j+1]
				d2 := dx*dx + dy*dy
				if d2 < 1 {
					d2 = 1
				}
				f := ForceRepulsion / d2
				forceX[i] += f * dx / math.Sqrt(d2)
				forceY[i] += f * dy / math.Sqrt(d2)
				forceX[j] -= f * dx / math.Sqrt(d2)
				forceY[j] -= f * dy / math.Sqrt(d2)
			}
		}

		// Attraction along the edges.
		for _, e := range graph.Edges {
			si, ok := indexOf(ids, e.Source)
			if !ok {
				continue
			}
			ti, ok := indexOf(ids, e.Target)
			if !ok {
				continue
			}
			dx := pos[2*ti] - pos[2*si]
			dy := pos[2*ti+1] - pos[2*si+1]
			forceX[si] += ForceAttraction * dx
			forceY[si] += ForceAttraction * dy
			forceX[ti] -= ForceAttraction * dx
			forceY[ti] -= ForceAttraction * dy
		}

		// Vertical gravity by depth, so the hierarchy reads top to bottom.
		for i, id := range ids {
			targetY := float64(levels[id]) * LayerGap
			if levels[id] < 0 {
				targetY = float64(maxLevel(graph)) * LayerGap
			}
			forceY[i] += (targetY - pos[2*i+1]) * 0.01
		}

		for i := range ids {
			pos[2*i] += clamp(forceX[i], -50, 50)
			pos[2*i+1] += clamp(forceY[i], -50, 50)
		}
	}

	// Normalise into the positive viewport.
	minX, minY := pos[0], pos[1]
	for i := range ids {
		if pos[2*i] < minX {
			minX = pos[2*i]
		}
		if pos[2*i+1] < minY {
			minY = pos[2*i+1]
		}
	}
	out := make([]Placement, 0, n)
	for i, id := range ids {
		out = append(out, Placement{
			NodeID: id,
			X:      round(pos[2*i] - minX),
			Y:      round(pos[2*i+1] - minY),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// adjacency returns the neighbour sets of the undirected graph.
func adjacency(graph *Graph) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(a, b string) {
		if a == "" || b == "" {
			return
		}
		if out[a] == nil {
			out[a] = map[string]bool{}
		}
		out[a][b] = true
	}
	for _, e := range graph.Edges {
		add(e.Source, e.Target)
		add(e.Target, e.Source)
	}
	return out
}

// computeLevels returns the depth of every node, or -1 when the node is not
// reachable from a root (a cycle, or a node whose parent is missing).
func computeLevels(graph *Graph) map[string]int {
	hasParent := map[string]bool{}
	for _, n := range graph.Nodes {
		if n.Parent != "" && n.Parent != n.ID {
			hasParent[n.ID] = true
		}
	}
	levels := map[string]int{}
	var resolve func(id string, depth int)
	seen := map[string]bool{}
	resolve = func(id string, depth int) {
		if seen[id] {
			return
		}
		seen[id] = true
		levels[id] = depth
		for _, n := range graph.Nodes {
			if n.Parent == id {
				resolve(n.ID, depth+1)
			}
		}
	}
	for _, n := range graph.Nodes {
		if !hasParent[n.ID] {
			resolve(n.ID, 0)
		}
	}
	for _, n := range graph.Nodes {
		if _, ok := levels[n.ID]; !ok {
			levels[n.ID] = -1
		}
	}
	return levels
}

func maxLevel(graph *Graph) int {
	max := 0
	for _, l := range computeLevels(graph) {
		if l > max {
			max = l
		}
	}
	return max
}

func indexOf(ids []string, want string) (int, bool) {
	for i, id := range ids {
		if id == want {
			return i, true
		}
	}
	return 0, false
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// round snaps to a whole pixel so persisted coordinates stay stable.
func round(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v + 0.5))
	}
	return float64(int64(v + 0.5))
}

// pi, cos, sin and sqrt are thin wrappers that keep the force layout readable
// without qualifying every call with the math package.
func pi() float64            { return math.Pi }
func cos(v float64) float64  { return math.Cos(v) }
func sin(v float64) float64  { return math.Sin(v) }
func sqrt(v float64) float64 { return math.Sqrt(v) }
