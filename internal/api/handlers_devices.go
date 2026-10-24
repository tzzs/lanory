package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Handlers for /api/v1/health, /api/v1/overview and /api/v1/devices.

// handleHealth serves GET /api/v1/health.
//
// This is the only route that bypasses authentication: a watchdog, a reverse
// proxy or the OpenWrt procd health check must be able to probe liveness
// without a token. It reports process facts only, never network state.
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.HealthResponse{
		Status:        "ok",
		Version:       Version,
		DemoMode:      a.demoMode(),
		UptimeSeconds: int64(time.Since(a.startedAt).Seconds()),
	})
}

// handleOverview serves GET /api/v1/overview.
//
// Everything is derived from the store. Counts come from a single aggregate
// query; interfaces from the monitoring provider; networks are derived from
// stored interface addresses so they cannot disagree with the topology.
func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	counts, err := a.store.DeviceCounts()
	if err != nil {
		internalError(w, err)
		return
	}

	interfaces, err := a.ifaces.InterfaceStatus()
	if err != nil {
		internalError(w, err)
		return
	}
	if interfaces == nil {
		interfaces = []model.InterfaceStatus{}
	}

	networks, err := a.networkStatuses()
	if err != nil {
		internalError(w, err)
		return
	}

	recentNew, _, err := a.store.ListDevices(database.DeviceFilter{
		Sort:      "last_seen",
		Order:     "desc",
		Limit:     5,
		NewerThan: database.Now().Add(-24 * time.Hour),
	})
	if err != nil {
		internalError(w, err)
		return
	}
	if recentNew == nil {
		recentNew = []model.Device{}
	}

	recentOffline, _, err := a.store.ListDevices(database.DeviceFilter{
		States: []model.DeviceState{model.StateOffline},
		Sort:   "last_seen",
		Order:  "desc",
		Limit:  5,
	})
	if err != nil {
		internalError(w, err)
		return
	}
	if recentOffline == nil {
		recentOffline = []model.Device{}
	}

	recentEvents, err := a.store.RecentEvents(10)
	if err != nil {
		internalError(w, err)
		return
	}
	if recentEvents == nil {
		recentEvents = []model.NetworkEvent{}
	}

	anomalies := a.anomalies(interfaces, counts)

	lastScan := time.Time{}
	if status, err := a.discovery.Status(); err == nil && status.LastJob != nil {
		lastScan = status.LastJob.FinishedAt
	}

	writeJSON(w, http.StatusOK, model.Overview{
		Counts:           counts,
		Interfaces:       interfaces,
		Networks:         networks,
		RecentNew:        recentNew,
		RecentOffline:    recentOffline,
		RecentEvents:     recentEvents,
		Anomalies:        anomalies,
		LastScanAt:       lastScan,
		DiscoveryEnabled: a.cfg.Discovery.Enabled,
		DemoMode:         a.demoMode(),
		GeneratedAt:      database.Now(),
	})
	_ = ctx
}

// anomalies derives the problems worth surfacing on the overview page.
//
// Only conditions that follow directly from stored state are reported: an
// interface with no carrier, a device that went offline, and a database that
// has never been scanned. No synthetic "network health score" is invented.
func (a *App) anomalies(interfaces []model.InterfaceStatus, counts model.DeviceCounts) []model.Anomaly {
	out := []model.Anomaly{}
	now := database.Now()

	for _, is := range interfaces {
		if is.Status == model.EdgeStatusLinkDown {
			out = append(out, model.Anomaly{
				ID:       "anom-link-" + is.Interface.ID,
				Severity: "warning",
				Title:    is.Interface.Name + " has no carrier",
				Detail:   "The interface is administratively " + string(is.Interface.AdminState) + " but the physical link is down.",
				Since:    is.Interface.LastSeen,
			})
		}
	}
	if counts.Offline > 0 {
		out = append(out, model.Anomaly{
			ID:       "anom-offline",
			Severity: "warning",
			Title:    strconv.Itoa(counts.Offline) + " device(s) offline",
			Detail:   "Devices that failed several consecutive reachability checks.",
			Since:    now,
		})
	}
	if counts.Unknown > 0 {
		out = append(out, model.Anomaly{
			ID:       "anom-unknown",
			Severity: "info",
			Title:    strconv.Itoa(counts.Unknown) + " device(s) have unknown state",
			Detail:   "Not enough evidence to say whether these devices are reachable. Enable monitoring to collect it.",
			Since:    now,
		})
	}
	return out
}

// networkStatuses derives the logical networks from stored interface addresses.
func (a *App) networkStatuses() ([]model.NetworkStatus, error) {
	devices, _, err := a.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 500})
	if err != nil {
		return nil, err
	}
	ifaces, err := listAllInterfaces(a.store)
	if err != nil {
		return nil, err
	}

	// Count devices per /24 so a network node can show its size.
	perNet := map[string]int{}
	for _, d := range devices {
		if d.PrimaryIPv4 == "" {
			continue
		}
		addr, err := ParseIPv4(d.PrimaryIPv4)
		if err != nil {
			continue
		}
		perNet[cidr24(addr.As4())]++
	}

	seen := map[string]bool{}
	out := []model.NetworkStatus{}
	for i := range devices {
		d := &devices[i]
		if d.PrimaryIPv4 == "" {
			continue
		}
		addr, err := ParseIPv4(d.PrimaryIPv4)
		if err != nil {
			continue
		}
		cidr := cidr24(addr.As4())
		if seen[cidr] {
			continue
		}
		seen[cidr] = true

		interfaceID := ""
		role := model.InterfaceRoleLAN
		for _, iface := range ifaces {
			if iface.DeviceID == d.ID && iface.Name != "" {
				interfaceID = iface.ID
				if iface.Role != "" && iface.Role != model.InterfaceRoleUnknown {
					role = iface.Role
				}
				break
			}
		}
		out = append(out, model.NetworkStatus{
			ID:          "net-" + strings.ReplaceAll(addr.String(), ".", "-"),
			Name:        networkName(d),
			InterfaceID: interfaceID,
			CIDR:        cidr,
			GatewayIP:   addr.String(),
			Role:        role,
			DeviceCount: perNet[cidr],
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// handleListDevices serves GET /api/v1/devices.
func (a *App) handleListDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pagination(r, DefaultLimit, MaxLimit)
	if err != nil {
		badRequest(w, err.Error())
		return
	}

	filter := database.DeviceFilter{
		Query:         strings.TrimSpace(q.Get("query")),
		Source:        strings.TrimSpace(q.Get("source")),
		Interface:     strings.TrimSpace(q.Get("interface")),
		Sort:          strings.TrimSpace(q.Get("sort")),
		Order:         strings.TrimSpace(q.Get("order")),
		OnlineFirst:   q.Get("online_first") == "true",
		Limit:         limit,
		Offset:        offset,
		IncludeHidden: q.Get("include_hidden") == "true",
	}

	if v := strings.TrimSpace(q.Get("type")); v != "" {
		types, err := parseDeviceTypes(v)
		if err != nil {
			badRequestField(w, "type", err.Error())
			return
		}
		filter.Types = types
	}
	if v := strings.TrimSpace(q.Get("state")); v != "" {
		states, err := parseDeviceStates(v)
		if err != nil {
			badRequestField(w, "state", err.Error())
			return
		}
		filter.States = states
	}
	if v := strings.TrimSpace(q.Get("new_since")); v != "" {
		t, err := ParseTime(v)
		if err != nil {
			badRequestField(w, "new_since", err.Error())
			return
		}
		filter.NewerThan = t
	}
	switch filter.Sort {
	case "", "name", "ip", "type", "last_seen":
	default:
		badRequestField(w, "sort", "sort must be one of name, ip, type, last_seen")
		return
	}
	switch filter.Order {
	case "", "asc", "desc":
	default:
		badRequestField(w, "order", "order must be asc or desc")
		return
	}

	devices, total, err := a.store.ListDevices(filter)
	if err != nil {
		internalError(w, err)
		return
	}
	if devices == nil {
		devices = []model.Device{}
	}
	for i := range devices {
		normalizeDevice(&devices[i])
	}

	writeJSON(w, http.StatusOK, model.DeviceList{
		Devices: devices,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	})
}

// parseDeviceTypes splits a comma separated list of device types.
func parseDeviceTypes(v string) ([]model.DeviceType, error) {
	var out []model.DeviceType
	for _, part := range strings.Split(v, ",") {
		t := model.DeviceType(strings.TrimSpace(part))
		if t == "" {
			continue
		}
		if !model.IsValidDeviceType(t) {
			return nil, errInvalidType(t)
		}
		out = append(out, t)
	}
	return out, nil
}

// errInvalidType builds a validation error for an unknown device type.
func errInvalidType(t model.DeviceType) error {
	return invalid("unknown device type %q", t)
}

// parseDeviceStates splits a comma separated list of device states.
func parseDeviceStates(v string) ([]model.DeviceState, error) {
	var out []model.DeviceState
	for _, part := range strings.Split(v, ",") {
		s := model.DeviceState(strings.TrimSpace(part))
		if s == "" {
			continue
		}
		switch s {
		case model.StateOnline, model.StateOffline, model.StateUnknown, model.StateSleeping:
		default:
			return nil, invalid("unknown device state %q", s)
		}
		out = append(out, s)
	}
	return out, nil
}

// createDeviceRequest is the body of POST /api/v1/devices.
type createDeviceRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Hostname          string `json:"hostname"`
	IPv4              string `json:"ipv4"`
	IPv6              string `json:"ipv6"`
	MAC               string `json:"mac"`
	Manufacturer      string `json:"manufacturer"`
	Model             string `json:"model"`
	Description       string `json:"description"`
	ParentDeviceID    string `json:"parent_device_id"`
	ConnectionType    string `json:"connection_type"`
	ParentInterfaceID string `json:"parent_interface_id"`
}

// handleCreateDevice serves POST /api/v1/devices.
//
// Devices created here are manual: discovery never re-creates or removes them.
// They start in state "unknown" unless a probe says otherwise, because a device
// the user just described has not been observed yet.
func (a *App) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var body createDeviceRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		badRequestField(w, "name", "name must not be empty")
		return
	}
	if len(name) > 128 {
		badRequestField(w, "name", "name must be at most 128 characters")
		return
	}

	devType := model.DeviceType(strings.TrimSpace(body.Type))
	if devType == "" {
		devType = model.DeviceTypeUnknown
	}
	if !model.IsValidDeviceType(devType) {
		badRequestField(w, "type", "unknown device type "+strconv.Quote(string(devType)))
		return
	}

	var mac, macCanonical string
	if body.MAC != "" {
		canonical, err := NormalizeMAC(body.MAC)
		if err != nil {
			badRequestField(w, "mac", err.Error())
			return
		}
		// The identity is stored canonically (aabbccddeeff) because that is
		// what the database layer matches on, while the cached display field
		// keeps the conventional colon separated form.
		macCanonical = canonical
		mac = FormatMAC(canonical)
	}

	var ipv4 string
	if strings.TrimSpace(body.IPv4) != "" {
		addr, err := ParseIPv4(body.IPv4)
		if err != nil {
			badRequestField(w, "ipv4", err.Error())
			return
		}
		ipv4 = addr.String()
	}

	var ipv6 string
	if strings.TrimSpace(body.IPv6) != "" {
		addr, err := ParseIPv6(body.IPv6)
		if err != nil {
			badRequestField(w, "ipv6", err.Error())
			return
		}
		ipv6 = addr.String()
	}

	if body.ParentDeviceID != "" {
		if _, err := a.store.GetDevice(body.ParentDeviceID); err != nil {
			storeError(w, err, "parent device")
			return
		}
	}
	if body.ParentInterfaceID != "" {
		if _, err := getInterface(a.store, body.ParentInterfaceID); err != nil {
			storeError(w, err, "parent interface")
			return
		}
	}

	connType := model.EdgeTypeUserDefined
	if strings.TrimSpace(body.ConnectionType) != "" {
		connType = model.EdgeType(strings.TrimSpace(body.ConnectionType))
		switch connType {
		case model.EdgeTypePhysicalLink, model.EdgeTypeUserDefined, model.EdgeTypeLogicalNetwork:
		default:
			badRequestField(w, "connection_type", "connection_type must be physical_link, user_defined or logical_network")
			return
		}
	}

	now := database.Now()
	dev := &model.Device{
		ID:               newManualDeviceID(mac, ipv4),
		Name:             name,
		Hostname:         strings.TrimSpace(body.Hostname),
		Type:             devType,
		Manufacturer:     strings.TrimSpace(body.Manufacturer),
		Model:            strings.TrimSpace(body.Model),
		Description:      strings.TrimSpace(body.Description),
		IsManual:         true,
		TypeSource:       model.SourceManual,
		State:            model.StateUnknown,
		StateReason:      "not yet probed",
		LastStateChange:  now,
		PrimaryIPv4:      ipv4,
		PrimaryIPv6:      ipv6,
		PrimaryMAC:       mac,
		FirstSeen:        now,
		LastSeen:         now,
		CreatedAt:        now,
		UpdatedAt:        now,
		DiscoverySources: []model.DiscoverySource{model.SourceManual},
	}
	if err := a.store.UpsertDevice(dev); err != nil {
		internalError(w, err)
		return
	}
	if macCanonical != "" {
		_ = a.store.UpsertIdentity(&model.DeviceIdentity{
			DeviceID:   dev.ID,
			Type:       model.IdentityMAC,
			Value:      macCanonical,
			Source:     model.SourceManual,
			Confidence: 1.0,
			FirstSeen:  now,
			LastSeen:   now,
		})
	}
	if ipv4 != "" {
		_ = a.store.UpsertAddress(&model.DeviceAddress{
			DeviceID:  dev.ID,
			Address:   ipv4,
			Family:    model.AddressFamilyIPv4,
			Source:    model.SourceManual,
			IsPrimary: true,
			FirstSeen: now,
			LastSeen:  now,
		})
	}
	if ipv6 != "" {
		_ = a.store.UpsertAddress(&model.DeviceAddress{
			DeviceID:  dev.ID,
			Address:   ipv6,
			Family:    model.AddressFamilyIPv6,
			Source:    model.SourceManual,
			IsPrimary: ipv4 == "",
			FirstSeen: now,
			LastSeen:  now,
		})
	}
	_ = a.store.AddDeviceSource(dev.ID, model.SourceManual)

	// A manual device with a parent gets a user defined edge, which is the
	// strongest relation the topology engine knows.
	if body.ParentDeviceID != "" {
		edge := &model.TopologyEdge{
			SourceID:          body.ParentDeviceID,
			TargetID:          dev.ID,
			SourceInterfaceID: body.ParentInterfaceID,
			EdgeType:          connType,
			DiscoverySource:   model.SourceManual,
			Confidence:        1.0,
			UserConfirmed:     true,
			Status:            model.EdgeStatusUnverified,
		}
		if err := a.store.UpsertEdge(edge); err == nil && a.bus != nil {
			a.bus.PublishLinkUpdated(edge, "manual device created")
		}
	}

	a.refreshKnownTargets()
	if a.bus != nil {
		a.bus.PublishDeviceUpdated(dev, "created manually")
	}

	created, err := a.store.GetDevice(dev.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// newManualDeviceID builds a stable ID for a manually created device.
func newManualDeviceID(mac, ipv4 string) string {
	switch {
	case mac != "":
		return "dev-mac-" + mac
	case ipv4 != "":
		return "dev-manual-" + strings.ReplaceAll(ipv4, ".", "-")
	default:
		return "dev-manual-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
}

// handleGetDevice serves GET /api/v1/devices/{id}.
func (a *App) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	detail, err := deviceDetail(a.store, id)
	if err != nil {
		storeError(w, err, "device")
		return
	}
	normalizeDeviceDetail(detail)
	writeJSON(w, http.StatusOK, detail)
}

// normalizeDevice replaces nil slices on a device with empty ones so the JSON
// contract ("empty lists are []") holds. A nil Go slice marshals as null, and
// clients read these fields as arrays.
func normalizeDevice(d *model.Device) {
	if d.DiscoverySources == nil {
		d.DiscoverySources = []model.DiscoverySource{}
	}
}

// normalizeDeviceDetail replaces nil slices with empty ones so the JSON
// contract ("empty lists are []") holds even for a device with no addresses.
func normalizeDeviceDetail(d *model.DeviceDetail) {
	if d.Device != nil {
		normalizeDevice(d.Device)
	}
	if d.Addresses == nil {
		d.Addresses = []model.DeviceAddress{}
	}
	if d.Identities == nil {
		d.Identities = []model.DeviceIdentity{}
	}
	if d.Interfaces == nil {
		d.Interfaces = []model.NetworkInterface{}
	}
	if d.Edges == nil {
		d.Edges = []model.TopologyEdge{}
	}
	if d.Probes == nil {
		d.Probes = []model.ProbeTarget{}
	}
	if d.Events == nil {
		d.Events = []model.NetworkEvent{}
	}
}

// patchDeviceRequest is the partial device accepted by PATCH.
type patchDeviceRequest struct {
	Name         *string `json:"name"`
	Type         *string `json:"type"`
	Hostname     *string `json:"hostname"`
	Manufacturer *string `json:"manufacturer"`
	Model        *string `json:"model"`
	Description  *string `json:"description"`
	IsLocked     *bool   `json:"is_locked"`
	IsHidden     *bool   `json:"is_hidden"`
}

// handlePatchDevice serves PATCH /api/v1/devices/{id}.
func (a *App) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	var body patchDeviceRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}

	patch := map[string]any{}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			badRequestField(w, "name", "name must not be empty")
			return
		}
		patch["name"] = name
	}
	if body.Type != nil {
		t := model.DeviceType(strings.TrimSpace(*body.Type))
		if !model.IsValidDeviceType(t) {
			badRequestField(w, "type", "unknown device type "+strconv.Quote(string(t)))
			return
		}
		patch["type"] = string(t)
	}
	if body.Hostname != nil {
		h := strings.TrimSpace(*body.Hostname)
		if h != "" {
			if _, err := ValidateHostname(h); err != nil {
				badRequestField(w, "hostname", err.Error())
				return
			}
		}
		patch["hostname"] = h
	}
	if body.Manufacturer != nil {
		patch["manufacturer"] = strings.TrimSpace(*body.Manufacturer)
	}
	if body.Model != nil {
		patch["model"] = strings.TrimSpace(*body.Model)
	}
	if body.Description != nil {
		patch["description"] = strings.TrimSpace(*body.Description)
	}
	if body.IsLocked != nil {
		patch["is_locked"] = *body.IsLocked
	}
	if body.IsHidden != nil {
		patch["is_hidden"] = *body.IsHidden
	}
	if len(patch) == 0 {
		badRequest(w, "no updatable fields in request body")
		return
	}

	dev, err := a.store.UpdateDevice(id, patch)
	if err != nil {
		storeError(w, err, "device")
		return
	}
	a.refreshKnownTargets()
	if a.bus != nil {
		a.bus.PublishDeviceUpdated(dev, "updated by user")
	}
	writeJSON(w, http.StatusOK, dev)
}

// handleDeleteDevice serves DELETE /api/v1/devices/{id}.
//
// mode=hide (the default for discovered devices) keeps the history and stops
// discovery from resurrecting the device. mode=delete removes the device and
// its history and is refused for discovered devices: silently destroying
// observations the user did not create is not recoverable.
func (a *App) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = "hide"
	}
	if mode != "hide" && mode != "delete" {
		badRequestField(w, "mode", "mode must be hide or delete")
		return
	}

	dev, err := a.store.GetDevice(id)
	if err != nil {
		storeError(w, err, "device")
		return
	}

	if mode == "delete" {
		if !dev.IsManual {
			conflict(w, "only manually created devices can be deleted; use mode=hide to keep the history")
			return
		}
		if err := a.store.DeleteDevice(id); err != nil {
			storeError(w, err, "device")
			return
		}
	} else {
		if err := a.store.HideDevice(id); err != nil {
			storeError(w, err, "device")
			return
		}
	}

	a.refreshKnownTargets()
	if a.bus != nil {
		a.bus.PublishDeviceUpdated(dev, "deleted ("+mode+")")
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMergeDevice serves POST /api/v1/devices/{id}/merge.
//
// The source device is folded into the destination and removed. This is the
// manual escape hatch for a duplicate the identity engine could not resolve.
func (a *App) handleMergeDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	var body struct {
		SourceDeviceID string `json:"source_device_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}
	src := strings.TrimSpace(body.SourceDeviceID)
	if src == "" {
		badRequestField(w, "source_device_id", "source_device_id must not be empty")
		return
	}
	if src == id {
		conflict(w, "cannot merge a device into itself")
		return
	}
	if _, err := a.store.GetDevice(id); err != nil {
		storeError(w, err, "device")
		return
	}
	if _, err := a.store.GetDevice(src); err != nil {
		storeError(w, err, "device")
		return
	}

	if err := a.store.MergeDevices(id, src); err != nil {
		internalError(w, err)
		return
	}
	merged, err := a.store.GetDevice(id)
	if err != nil {
		storeError(w, err, "device")
		return
	}
	a.refreshKnownTargets()
	if a.bus != nil {
		a.bus.PublishDeviceUpdated(merged, "merged with "+src)
		a.bus.PublishTopologyUpdated("devices merged")
	}
	writeJSON(w, http.StatusOK, merged)
}
