package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Handlers for /api/v1/topology.

// handleTopology serves GET /api/v1/topology.
//
// The graph comes from the injected TopologyProvider. The store-only fallback
// provider (providers_stub.go) returns exactly the devices, edges and layout
// that were persisted — no inferred relations.
func (a *App) handleTopology(w http.ResponseWriter, r *http.Request) {
	topo, err := a.topo.Build()
	if err != nil {
		internalError(w, err)
		return
	}
	if topo == nil {
		topo = &model.Topology{GeneratedAt: database.Now()}
	}
	if topo.Nodes == nil {
		topo.Nodes = []model.TopologyNode{}
	}
	if topo.Edges == nil {
		topo.Edges = []model.TopologyEdge{}
	}
	if topo.Layout == nil {
		topo.Layout = []model.TopologyLayout{}
	}
	// Nodes embed a Device, whose own lists would otherwise marshal as null.
	for i := range topo.Nodes {
		if topo.Nodes[i].Device != nil {
			normalizeDevice(topo.Nodes[i].Device)
		}
	}
	writeJSON(w, http.StatusOK, topo)
}

// handleTopologyEvidence serves GET /api/v1/topology/evidence.
func (a *App) handleTopologyEvidence(w http.ResponseWriter, r *http.Request) {
	ev, err := a.topo.Evidence()
	if err != nil {
		internalError(w, err)
		return
	}
	if ev == nil {
		ev = &model.TopologyEvidence{}
	}
	if ev.Edges == nil {
		ev.Edges = []model.EdgeEvidence{}
	}
	if ev.Unmapped == nil {
		ev.Unmapped = []model.DeviceEvidence{}
	}
	// Evidence entries embed a Device too.
	for i := range ev.Unmapped {
		normalizeDevice(&ev.Unmapped[i].Device)
	}
	writeJSON(w, http.StatusOK, ev)
}

// createEdgeRequest is the body of POST /api/v1/topology/edges.
type createEdgeRequest struct {
	SourceID          string `json:"source_id"`
	TargetID          string `json:"target_id"`
	EdgeType          string `json:"edge_type"`
	SourceInterfaceID string `json:"source_interface_id"`
	TargetInterfaceID string `json:"target_interface_id"`
	Label             string `json:"label"`
}

// handleCreateEdge serves POST /api/v1/topology/edges.
//
// A user asserted relation gets confidence 1.0 and user_confirmed true: it is
// the strongest evidence the system has and must not be overwritten by a later
// guess. Both endpoints must exist, must be distinct devices, and the edge must
// not already exist (that is a 409, not a silent upsert).
func (a *App) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	var body createEdgeRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}

	src := strings.TrimSpace(body.SourceID)
	tgt := strings.TrimSpace(body.TargetID)
	if src == "" {
		badRequestField(w, "source_id", "source_id must not be empty")
		return
	}
	if tgt == "" {
		badRequestField(w, "target_id", "target_id must not be empty")
		return
	}
	if src == tgt {
		conflict(w, "an edge cannot connect a device to itself")
		return
	}
	if _, err := a.store.GetDevice(src); err != nil {
		storeError(w, err, "source device")
		return
	}
	if _, err := a.store.GetDevice(tgt); err != nil {
		storeError(w, err, "target device")
		return
	}

	edgeType := model.EdgeType(strings.TrimSpace(body.EdgeType))
	if edgeType == "" {
		edgeType = model.EdgeTypeUserDefined
	}
	switch edgeType {
	case model.EdgeTypePhysicalLink, model.EdgeTypeBridgeMembership, model.EdgeTypeObservedMACPort,
		model.EdgeTypeWirelessAssoc, model.EdgeTypeLogicalNetwork, model.EdgeTypeUserDefined:
	default:
		badRequestField(w, "edge_type", "unknown edge type "+strconv.Quote(string(edgeType)))
		return
	}

	srcIface := strings.TrimSpace(body.SourceInterfaceID)
	if srcIface != "" {
		if _, err := getInterface(a.store, srcIface); err != nil {
			storeError(w, err, "source interface")
			return
		}
	}
	tgtIface := strings.TrimSpace(body.TargetInterfaceID)
	if tgtIface != "" {
		if _, err := getInterface(a.store, tgtIface); err != nil {
			storeError(w, err, "target interface")
			return
		}
	}

	// A relation the user declared must not be silently merged into an existing
	// edge of a different type: that would change what the user asserted.
	if existing, err := a.store.GetEdgeByTuple(src, tgt, srcIface, tgtIface, edgeType); err == nil && existing != nil {
		conflict(w, "an edge with these endpoints and type already exists")
		return
	}
	if existing, err := a.store.GetEdgeByTuple(tgt, src, tgtIface, srcIface, edgeType); err == nil && existing != nil {
		conflict(w, "an edge with these endpoints and type already exists in the opposite direction")
		return
	}

	now := database.Now()
	edge := &model.TopologyEdge{
		SourceID:          src,
		TargetID:          tgt,
		SourceInterfaceID: srcIface,
		TargetInterfaceID: tgtIface,
		EdgeType:          edgeType,
		DiscoverySource:   model.SourceManual,
		Confidence:        1.0,
		UserConfirmed:     true,
		Status:            model.EdgeStatusUnverified,
		Label:             strings.TrimSpace(body.Label),
		LastObserved:      now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := a.store.UpsertEdge(edge); err != nil {
		internalError(w, err)
		return
	}

	if a.bus != nil {
		a.bus.PublishLinkUpdated(edge, "edge created by user")
		a.bus.PublishTopologyUpdated("edge created")
	}
	writeJSON(w, http.StatusCreated, edge)
}

// handleGetEdge serves GET /api/v1/topology/edges/{id}.
//
// Not in the documented contract but useful for the UI: a client that has an
// edge ID can fetch it without downloading the whole graph.
func (a *App) handleGetEdge(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	edge, err := a.store.GetEdge(id)
	if err != nil {
		storeError(w, err, "edge")
		return
	}
	writeJSON(w, http.StatusOK, edge)
}

// patchEdgeRequest is the partial edge accepted by PATCH.
type patchEdgeRequest struct {
	EdgeType          *string  `json:"edge_type"`
	Confidence        *float64 `json:"confidence"`
	UserConfirmed     *bool    `json:"user_confirmed"`
	Locked            *bool    `json:"locked"`
	Status            *string  `json:"status"`
	Label             *string  `json:"label"`
	SourceInterfaceID *string  `json:"source_interface_id"`
	TargetInterfaceID *string  `json:"target_interface_id"`
}

// handlePatchEdge serves PATCH /api/v1/topology/edges/{id}.
func (a *App) handlePatchEdge(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	var body patchEdgeRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}

	patch := map[string]any{}
	if body.EdgeType != nil {
		t := model.EdgeType(strings.TrimSpace(*body.EdgeType))
		switch t {
		case model.EdgeTypePhysicalLink, model.EdgeTypeBridgeMembership, model.EdgeTypeObservedMACPort,
			model.EdgeTypeWirelessAssoc, model.EdgeTypeLogicalNetwork, model.EdgeTypeUserDefined:
		default:
			badRequestField(w, "edge_type", "unknown edge type "+strconv.Quote(string(t)))
			return
		}
		patch["edge_type"] = string(t)
	}
	if body.Confidence != nil {
		if *body.Confidence < 0 || *body.Confidence > 1 {
			badRequestField(w, "confidence", "confidence must be between 0 and 1")
			return
		}
		patch["confidence"] = *body.Confidence
	}
	if body.UserConfirmed != nil {
		patch["user_confirmed"] = *body.UserConfirmed
	}
	if body.Locked != nil {
		patch["locked"] = *body.Locked
	}
	if body.Status != nil {
		s := model.EdgeStatus(strings.TrimSpace(*body.Status))
		switch s {
		case model.EdgeStatusLinkUp, model.EdgeStatusLinkDown, model.EdgeStatusReachable,
			model.EdgeStatusUnreachable, model.EdgeStatusUnverified:
		default:
			badRequestField(w, "status", "unknown edge status "+strconv.Quote(string(s)))
			return
		}
		patch["status"] = string(s)
	}
	if body.Label != nil {
		patch["label"] = strings.TrimSpace(*body.Label)
	}
	if body.SourceInterfaceID != nil {
		v := strings.TrimSpace(*body.SourceInterfaceID)
		if v != "" {
			if _, err := getInterface(a.store, v); err != nil {
				storeError(w, err, "source interface")
				return
			}
		}
		patch["source_interface_id"] = v
	}
	if body.TargetInterfaceID != nil {
		v := strings.TrimSpace(*body.TargetInterfaceID)
		if v != "" {
			if _, err := getInterface(a.store, v); err != nil {
				storeError(w, err, "target interface")
				return
			}
		}
		patch["target_interface_id"] = v
	}
	if len(patch) == 0 {
		badRequest(w, "no updatable fields in request body")
		return
	}

	edge, err := a.store.UpdateEdge(id, patch)
	if err != nil {
		storeError(w, err, "edge")
		return
	}
	if a.bus != nil {
		a.bus.PublishLinkUpdated(edge, "edge updated by user")
	}
	writeJSON(w, http.StatusOK, edge)
}

// handleDeleteEdge serves DELETE /api/v1/topology/edges/{id}.
//
// A locked edge is protected: locking is how a user says "stop touching this
// relation", so deleting it is a conflict rather than a silent success.
func (a *App) handleDeleteEdge(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	edge, err := a.store.GetEdge(id)
	if err != nil {
		storeError(w, err, "edge")
		return
	}
	if edge.Locked {
		conflict(w, "edge is locked; unlock it before deleting")
		return
	}
	if err := a.store.DeleteEdge(id); err != nil {
		storeError(w, err, "edge")
		return
	}
	if a.bus != nil {
		a.bus.PublishLinkUpdated(edge, "edge deleted")
		a.bus.PublishTopologyUpdated("edge deleted")
	}
	w.WriteHeader(http.StatusNoContent)
}

// saveLayoutRequest is the body of POST /api/v1/topology/layout.
type saveLayoutRequest struct {
	Nodes []layoutNode `json:"nodes"`
}

// layoutNode is one persisted node position.
type layoutNode struct {
	NodeID string   `json:"node_id"`
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	Pinned *bool    `json:"pinned"`
}

// handleSaveLayout serves POST /api/v1/topology/layout.
//
// Positions are persisted for known topology nodes, including logical networks
// and unresolved segments. Raw device IDs are accepted for older API clients.
func (a *App) handleSaveLayout(w http.ResponseWriter, r *http.Request) {
	var body saveLayoutRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}
	if len(body.Nodes) == 0 {
		badRequestField(w, "nodes", "at least one node is required")
		return
	}
	if len(body.Nodes) > 500 {
		badRequestField(w, "nodes", "at most 500 nodes per request")
		return
	}

	graph, err := a.topo.Build()
	if err != nil {
		internalError(w, err)
		return
	}
	known := map[string]bool{}
	if graph != nil {
		for _, node := range graph.Nodes {
			known[node.ID] = true
		}
	}
	// Validate all node identities before changing any saved coordinates.
	for _, n := range body.Nodes {
		nodeID := strings.TrimSpace(n.NodeID)
		if nodeID == "" {
			badRequestField(w, "nodes", "node_id must not be empty")
			return
		}
		if !known[nodeID] {
			if _, err := a.store.GetDevice(nodeID); err != nil {
				storeError(w, err, "topology node")
				return
			}
		}
	}
	saved := 0
	for _, n := range body.Nodes {
		nodeID := strings.TrimSpace(n.NodeID)
		l := &model.TopologyLayout{NodeID: nodeID}
		if n.X != nil {
			l.X = *n.X
		}
		if n.Y != nil {
			l.Y = *n.Y
		}
		if n.Pinned != nil {
			l.Pinned = *n.Pinned
		}
		if err := a.store.SaveLayout(l); err != nil {
			internalError(w, err)
			return
		}
		saved++
	}
	writeJSON(w, http.StatusOK, map[string]int{"saved": saved})
}

// autoLayoutRequest is the body of POST /api/v1/topology/auto-layout.
type autoLayoutRequest struct {
	Algorithm string `json:"algorithm"`
}

// handleAutoLayout serves POST /api/v1/topology/auto-layout.
//
// Computing a layout is the topology engine's responsibility. The store-only
// fallback provider cannot do it, so against the stub this endpoint answers
// 501 rather than returning positions that were never computed; production
// injects the real engine.
func (a *App) handleAutoLayout(w http.ResponseWriter, r *http.Request) {
	var body autoLayoutRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}
	algorithm := strings.TrimSpace(body.Algorithm)
	if algorithm == "" {
		algorithm = "tree"
	}
	switch algorithm {
	case "tree", "layered", "force":
	default:
		badRequestField(w, "algorithm", "algorithm must be tree, layered or force")
		return
	}
	layout, err := a.topo.AutoLayout(algorithm)
	if err != nil {
		unwired(w, err)
		return
	}
	if layout == nil {
		layout = []model.TopologyLayout{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"layout": layout})
}

// sinceLimit reads the since/limit pair used by the history endpoints.
func sinceLimit(r *http.Request) (time.Time, int, error) {
	q := r.URL.Query()
	since := time.Time{}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		t, err := ParseTime(v)
		if err != nil {
			return time.Time{}, 0, err
		}
		since = t
	}
	if since.IsZero() {
		since = database.Now().Add(-24 * time.Hour)
	}
	limit := 100
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := parseIntParam(v, "limit")
		if err != nil {
			return time.Time{}, 0, err
		}
		limit = n
	}
	if limit > 500 {
		limit = 500
	}
	if limit < 1 {
		return time.Time{}, 0, invalid("limit must be >= 1")
	}
	return since, limit, nil
}
