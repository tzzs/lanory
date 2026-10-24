package api

import (
	"net/http"
	"strings"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Handlers for /api/v1/discovery and /api/v1/monitoring.

// handleDiscoveryStatus serves GET /api/v1/discovery/status.
func (a *App) handleDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	status, err := a.discovery.Status()
	if err != nil {
		internalError(w, err)
		return
	}
	if status == nil {
		status = &model.DiscoveryStatus{}
	}
	status.Enabled = a.cfg.Discovery.Enabled
	status.Interval = a.cfg.Discovery.Interval
	if status.Collectors == nil {
		status.Collectors = []model.CollectorStatus{}
	}
	writeJSON(w, http.StatusOK, status)
}

// handleDiscoveryCollectors serves GET /api/v1/discovery/collectors.
func (a *App) handleDiscoveryCollectors(w http.ResponseWriter, r *http.Request) {
	collectors, err := a.discovery.Collectors()
	if err != nil {
		internalError(w, err)
		return
	}
	if collectors == nil {
		collectors = []model.CollectorStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"collectors": collectors})
}

// handleStartScan serves POST /api/v1/discovery/scan.
//
// The requested subnets pass through the SSRF guard: a scan is a network side
// effect, so the same allow-list that protects probes protects it. An
// unauthenticated scan of the whole internet is exactly what the guard exists
// to prevent.
func (a *App) handleStartScan(w http.ResponseWriter, r *http.Request) {
	var body ScanRequest
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}

	if len(body.Subnets) > 32 {
		badRequestField(w, "subnets", "at most 32 subnets per scan")
		return
	}
	for _, s := range body.Subnets {
		normalized, _, err := a.ssrf.CheckTarget(strings.TrimSpace(s))
		if err != nil {
			badRequestField(w, "subnets", err.Error())
			return
		}
		_ = normalized
	}
	if len(body.Collectors) > 32 {
		badRequestField(w, "collectors", "at most 32 collectors per scan")
		return
	}

	job, err := a.discovery.Scan(r.Context(), body)
	if err != nil {
		unwired(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// handleDiscoveryJob serves GET /api/v1/discovery/jobs/{id}.
func (a *App) handleDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	job, err := a.discovery.Job(id)
	if err != nil {
		unwired(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleCancelDiscoveryJob serves POST /api/v1/discovery/jobs/{id}/cancel.
func (a *App) handleCancelDiscoveryJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := a.discovery.Cancel(id); err != nil {
		unwired(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// handleMonitoringStatus serves GET /api/v1/monitoring/status.
func (a *App) handleMonitoringStatus(w http.ResponseWriter, r *http.Request) {
	status, err := a.monitoring.Status()
	if err != nil {
		internalError(w, err)
		return
	}
	if status == nil {
		status = &model.MonitoringStatus{GeneratedAt: database.Now()}
	}
	if status.Probes == nil {
		status.Probes = []model.ProbeStatus{}
	}
	// Each probe's Recent is a list too, and a probe that has never run has
	// none. Left nil it marshals as null where the contract promises an array,
	// and the monitoring page reads recent.length on every probe.
	for i := range status.Probes {
		if status.Probes[i].Recent == nil {
			status.Probes[i].Recent = []model.ProbeResult{}
		}
	}
	if status.Interfaces == nil {
		status.Interfaces = []model.InterfaceStatus{}
	}
	writeJSON(w, http.StatusOK, status)
}

// handleListProbes serves GET /api/v1/monitoring/probes.
func (a *App) handleListProbes(w http.ResponseWriter, r *http.Request) {
	probes, err := a.store.ListProbes()
	if err != nil {
		internalError(w, err)
		return
	}
	if probes == nil {
		probes = []model.ProbeTarget{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"probes": probes})
}

// createProbeRequest is the body of POST /api/v1/monitoring/probes.
type createProbeRequest struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Target           string `json:"target"`
	DeviceID         string `json:"device_id"`
	Protocol         string `json:"protocol"`
	Port             *int   `json:"port"`
	Interval         *int   `json:"interval"`
	Timeout          *int   `json:"timeout"`
	FailureThreshold *int   `json:"failure_threshold"`
	Enabled          *bool  `json:"enabled"`
}

// handleCreateProbe serves POST /api/v1/monitoring/probes.
//
// The target goes through the SSRF guard: this is the single most dangerous
// input in the whole API, because it asks the server to open a connection.
// A device probe derives its target from the device's stored address, which is
// always allowed; a network probe must name a target the user is allowed to
// reach.
func (a *App) handleCreateProbe(w http.ResponseWriter, r *http.Request) {
	var body createProbeRequest
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

	kind := model.ProbeKind(strings.TrimSpace(body.Kind))
	if kind == "" {
		kind = model.ProbeKindDevice
	}
	switch kind {
	case model.ProbeKindDevice, model.ProbeKindNetwork:
	default:
		badRequestField(w, "kind", "kind must be device or network")
		return
	}

	protocol := model.ProbeProtocol(strings.TrimSpace(body.Protocol))
	if protocol == "" {
		protocol = model.ProbeICMP
	}
	switch protocol {
	case model.ProbeICMP, model.ProbeTCP, model.ProbeHTTP, model.ProbeDNS, model.ProbeARP:
	default:
		badRequestField(w, "protocol", "protocol must be icmp, tcp, http, dns or arp")
		return
	}

	interval := 60
	if body.Interval != nil {
		interval = *body.Interval
	}
	if interval < 5 || interval > 86400 {
		badRequestField(w, "interval", "interval must be between 5 and 86400 seconds")
		return
	}
	timeout := 3
	if body.Timeout != nil {
		timeout = *body.Timeout
	}
	if timeout < 1 || timeout > 60 {
		badRequestField(w, "timeout", "timeout must be between 1 and 60 seconds")
		return
	}
	failureThreshold := a.cfg.Monitoring.FailureThreshold
	if body.FailureThreshold != nil {
		failureThreshold = *body.FailureThreshold
	}
	if failureThreshold < 1 || failureThreshold > 100 {
		badRequestField(w, "failure_threshold", "failure_threshold must be between 1 and 100")
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	// Resolve the target.
	target := strings.TrimSpace(body.Target)
	deviceID := strings.TrimSpace(body.DeviceID)

	if deviceID != "" {
		dev, err := a.store.GetDevice(deviceID)
		if err != nil {
			storeError(w, err, "device")
			return
		}
		if target == "" {
			target = dev.PrimaryIPv4
			if target == "" {
				target = dev.PrimaryIPv6
			}
		}
		if target == "" {
			badRequestField(w, "target", "device has no known address to probe")
			return
		}
	}

	if target == "" {
		badRequestField(w, "target", "target is required (an IP address, a hostname of a known device, or an http(s) URL)")
		return
	}
	normalized, _, err := a.ssrf.CheckTarget(target)
	if err != nil {
		badRequestField(w, "target", err.Error())
		return
	}

	port := 0
	if body.Port != nil {
		if err := ValidatePort(*body.Port); err != nil {
			badRequestField(w, "port", err.Error())
			return
		}
		port = *body.Port
	}
	if (protocol == model.ProbeTCP || protocol == model.ProbeHTTP) && port == 0 {
		badRequestField(w, "port", "port is required for tcp and http probes")
		return
	}

	now := database.Now()
	probe := &model.ProbeTarget{
		Name:             name,
		Kind:             kind,
		Target:           normalized,
		Port:             port,
		Protocol:         protocol,
		SourceID:         "",
		Interval:         interval,
		Timeout:          timeout,
		Enabled:          enabled,
		DeviceID:         deviceID,
		FailureThreshold: failureThreshold,
		RecoverConfirm:   a.cfg.Monitoring.RecoverConfirm,
		State:            model.StateUnknown,
		StateReason:      "waiting for first probe",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := createProbe(a.store, probe); err != nil {
		internalError(w, err)
		return
	}
	if a.bus != nil {
		a.bus.PublishSettingsUpdated("monitoring.probes", probe.ID)
	}
	writeJSON(w, http.StatusCreated, probe)
}

// handleGetProbe serves GET /api/v1/monitoring/probes/{id}.
func (a *App) handleGetProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	probe, err := a.store.GetProbe(id)
	if err != nil {
		storeError(w, err, "probe")
		return
	}
	writeJSON(w, http.StatusOK, probe)
}

// patchProbeRequest is the partial probe accepted by PATCH.
type patchProbeRequest struct {
	Name             *string `json:"name"`
	Target           *string `json:"target"`
	Protocol         *string `json:"protocol"`
	Port             *int    `json:"port"`
	Interval         *int    `json:"interval"`
	Timeout          *int    `json:"timeout"`
	Enabled          *bool   `json:"enabled"`
	FailureThreshold *int    `json:"failure_threshold"`
}

// handlePatchProbe serves PATCH /api/v1/monitoring/probes/{id}.
func (a *App) handlePatchProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	existing, err := a.store.GetProbe(id)
	if err != nil {
		storeError(w, err, "probe")
		return
	}

	var body patchProbeRequest
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
	if body.Target != nil {
		normalized, _, err := a.ssrf.CheckTarget(strings.TrimSpace(*body.Target))
		if err != nil {
			badRequestField(w, "target", err.Error())
			return
		}
		patch["target"] = normalized
	}
	if body.Protocol != nil {
		p := model.ProbeProtocol(strings.TrimSpace(*body.Protocol))
		switch p {
		case model.ProbeICMP, model.ProbeTCP, model.ProbeHTTP, model.ProbeDNS, model.ProbeARP:
		default:
			badRequestField(w, "protocol", "protocol must be icmp, tcp, http, dns or arp")
			return
		}
		patch["protocol"] = string(p)
	}
	if body.Port != nil {
		if err := ValidatePort(*body.Port); err != nil {
			badRequestField(w, "port", err.Error())
			return
		}
		patch["port"] = *body.Port
	}
	if body.Interval != nil {
		if *body.Interval < 5 || *body.Interval > 86400 {
			badRequestField(w, "interval", "interval must be between 5 and 86400 seconds")
			return
		}
		patch["interval"] = *body.Interval
	}
	if body.Timeout != nil {
		if *body.Timeout < 1 || *body.Timeout > 60 {
			badRequestField(w, "timeout", "timeout must be between 1 and 60 seconds")
			return
		}
		patch["timeout"] = *body.Timeout
	}
	if body.Enabled != nil {
		patch["enabled"] = *body.Enabled
	}
	if body.FailureThreshold != nil {
		if *body.FailureThreshold < 1 || *body.FailureThreshold > 100 {
			badRequestField(w, "failure_threshold", "failure_threshold must be between 1 and 100")
			return
		}
		patch["failure_threshold"] = *body.FailureThreshold
	}
	if len(patch) == 0 {
		badRequest(w, "no updatable fields in request body")
		return
	}

	// A tcp/http probe needs a port, whichever field supplied it.
	protocol := existing.Protocol
	if body.Protocol != nil {
		protocol = model.ProbeProtocol(strings.TrimSpace(*body.Protocol))
	}
	port := existing.Port
	if body.Port != nil {
		port = *body.Port
	}
	if (protocol == model.ProbeTCP || protocol == model.ProbeHTTP) && port == 0 {
		badRequestField(w, "port", "port is required for tcp and http probes")
		return
	}

	probe, err := a.store.UpdateProbe(id, patch)
	if err != nil {
		storeError(w, err, "probe")
		return
	}
	writeJSON(w, http.StatusOK, probe)
}

// handleDeleteProbe serves DELETE /api/v1/monitoring/probes/{id}.
//
// Not in docs/api.md but the frontend calls it: a probe the user created must
// be removable, otherwise a typo becomes permanent.
func (a *App) handleDeleteProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := a.store.DeleteProbe(id); err != nil {
		storeError(w, err, "probe")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunProbe serves POST /api/v1/monitoring/probes/{id}/run.
//
// Executing a probe is a network side effect, so it needs the probe engine.
// Until that is wired the endpoint answers 501 rather than reporting a fake
// result.
func (a *App) handleRunProbe(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if _, err := a.store.GetProbe(id); err != nil {
		storeError(w, err, "probe")
		return
	}
	result, err := a.monitoring.Run(r.Context(), id)
	if err != nil {
		unwired(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleMonitoringHistory serves GET /api/v1/monitoring/history.
func (a *App) handleMonitoringHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, limit, err := sinceLimit(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}

	var results []model.ProbeResult
	switch {
	case strings.TrimSpace(q.Get("probe_id")) != "":
		probeID := strings.TrimSpace(q.Get("probe_id"))
		if _, err := a.store.GetProbe(probeID); err != nil {
			storeError(w, err, "probe")
			return
		}
		results, err = a.store.RecentProbeResults(probeID, limit)
	case strings.TrimSpace(q.Get("device_id")) != "":
		deviceID := strings.TrimSpace(q.Get("device_id"))
		probes, perr := a.store.ListProbesByDevice(deviceID)
		if perr != nil {
			internalError(w, perr)
			return
		}
		for _, p := range probes {
			recent, rerr := a.store.RecentProbeResults(p.ID, limit)
			if rerr != nil {
				internalError(w, rerr)
				return
			}
			results = append(results, recent...)
		}
	default:
		results, err = a.store.ProbeHistory(since, limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if results == nil {
		results = []model.ProbeResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
