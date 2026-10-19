package monitoring

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// Internet connectivity is decided from several independent targets, never from
// a single website. A single target can be down (or blocked, or geo-filtered)
// while the rest of the path is healthy, and treating one website as "the
// Internet" produces false alarms.
//
// The targets are grouped by role:
//
//	gateway   — the router itself (LAN side)
//	lan       — another host inside the LAN
//	external  — one or more hosts on the Internet
//	dns       — a DNS server
//
// The verdict is "healthy" only when every group has at least one reachable
// target. A group with no reachable target degrades the verdict; when the
// gateway (and therefore every LAN target) is unreachable while external
// targets are also unreachable, the verdict is "down".

// Internet verdict values. These strings are part of the API contract.
const (
	// InternetHealthy means every target group answered.
	InternetHealthy = "healthy"
	// InternetDegraded means at least one group failed while another answered.
	InternetDegraded = "degraded"
	// InternetDown means nothing answered.
	InternetDown = "down"
	// InternetUnknown means no internet target has ever been probed.
	InternetUnknown = "unknown"
)

// Internet role values.
const (
	InternetRoleGateway  = "gateway"
	InternetRoleLAN      = "lan"
	InternetRoleExternal = "external"
	InternetRoleDNS      = "dns"
)

// InternetTarget is one configured connectivity check.
type InternetTarget struct {
	// ID is the stable identifier of the check.
	ID string `json:"id"`
	// Name is the human readable label, e.g. "Cloudflare DNS".
	Name string `json:"name"`
	// Role classifies the check: gateway | lan | external | dns.
	Role string `json:"role"`
	// Target is the address, hostname, or "name@server" for DNS checks.
	Target string `json:"target"`
	// Protocol is one of icmp | tcp | http | dns.
	Protocol model.ProbeProtocol `json:"protocol"`
	// Port is used by tcp/http checks.
	Port int `json:"port,omitempty"`
	// Enabled disables a check without deleting it.
	Enabled bool `json:"enabled"`

	// Reachable is the last observed outcome.
	Reachable bool `json:"reachable"`
	// LatencyMS is the last observed latency.
	LatencyMS int `json:"latency_ms"`
	// Status is the last observed status.
	Status model.ProbeRunStatus `json:"status"`
	// ErrorType and ErrorMessage describe the last failure.
	ErrorType    string `json:"error_type,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	// LastCheckedAt is when the check last ran.
	LastCheckedAt time.Time `json:"last_checked_at"`
	// Source is the probe source that produced the result.
	Source string `json:"source"`
}

// InternetVerdict is the aggregated connectivity report.
//
// It is a plain struct on purpose: the API layer can render it for the Overview
// endpoint without depending on the scheduler.
type InternetVerdict struct {
	// Status is healthy | degraded | down | unknown.
	Status string `json:"status"`
	// Summary is a short human readable explanation.
	Summary string `json:"summary"`
	// Targets lists the per-target detail, in a stable order.
	Targets []InternetTarget `json:"targets"`
	// Groups reports per-role rollups.
	Groups []InternetGroup `json:"groups"`
	// Source is the probe source the verdict was computed from.
	Source string `json:"source"`
	// CheckedAt is when the last check ran.
	CheckedAt time.Time `json:"checked_at"`
}

// InternetGroup is the rollup of one role.
type InternetGroup struct {
	Role      string `json:"role"`
	Reachable int    `json:"reachable"`
	Total     int    `json:"total"`
}

// internetState is the engine's private bookkeeping for connectivity checks.
type internetState struct {
	mu      sync.Mutex
	targets map[string]*InternetTarget
}

// DefaultInternetTargets returns the checks a fresh install gets.
//
// They are conservative on purpose: an ICMP check against the gateway and the
// upstream resolver, a TCP check against a well known host and a DNS lookup.
// Users edit them from the UI; nothing is hardcoded as "the" internet test.
func DefaultInternetTargets(source string) []InternetTarget {
	if source == "" {
		source = DefaultSourceID
	}
	return []InternetTarget{
		{ID: "net-gateway", Name: "Gateway", Role: InternetRoleGateway, Target: "192.168.100.1", Protocol: model.ProbeICMP, Enabled: true, Source: source},
		{ID: "net-lan-host", Name: "LAN host", Role: InternetRoleLAN, Target: "192.0.2.116", Protocol: model.ProbeICMP, Enabled: true, Source: source},
		{ID: "net-dns", Name: "DNS", Role: InternetRoleDNS, Target: "lanory.invalid@192.168.100.1", Protocol: model.ProbeDNS, Enabled: true, Source: source},
		{ID: "net-ext-tcp", Name: "External TCP/443", Role: InternetRoleExternal, Target: "one.one.one.one", Protocol: model.ProbeTCP, Port: 443, Enabled: true, Source: source},
		{ID: "net-ext-dns", Name: "External DNS", Role: InternetRoleExternal, Target: "one.one.one.one", Protocol: model.ProbeDNS, Enabled: true, Source: source},
	}
}

// SetInternetTargets replaces the configured connectivity checks.
func (e *Engine) SetInternetTargets(targets []InternetTarget) {
	e.internet.mu.Lock()
	defer e.internet.mu.Unlock()
	e.internet.targets = map[string]*InternetTarget{}
	for i := range targets {
		t := targets[i]
		if t.ID == "" {
			continue
		}
		if t.Source == "" {
			t.Source = DefaultSourceID
		}
		e.internet.targets[t.ID] = &t
	}
}

// InternetTargets returns the configured checks, in a stable order.
func (e *Engine) InternetTargets() []InternetTarget {
	e.internet.mu.Lock()
	defer e.internet.mu.Unlock()
	out := make([]InternetTarget, 0, len(e.internet.targets))
	for _, t := range e.internet.targets {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CheckInternet runs every enabled connectivity target and returns the
// aggregated verdict.
//
// The targets are probed concurrently but each with its own timeout, so one
// black-holed host cannot stall the whole verdict.
//
// The verdict also feeds the device state machine: when even the LAN-side
// targets (gateway, LAN hosts) are unreachable, the probe source itself has lost
// the network. From then on device probe failures are attributed to the path
// rather than to the devices, so a router reboot or a pulled cable does not mark
// the whole household offline.
func (e *Engine) CheckInternet(ctx context.Context) InternetVerdict {
	targets := e.InternetTargets()
	if len(targets) == 0 {
		return InternetVerdict{
			Status:    InternetUnknown,
			Summary:   "no internet connectivity targets configured",
			Targets:   []InternetTarget{},
			Groups:    []InternetGroup{},
			Source:    DefaultSourceID,
			CheckedAt: time.Now().UTC(),
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	results := make([]InternetTarget, len(targets))
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.checkInternetTarget(ctx, targets[i])
		}(i)
	}
	wg.Wait()

	e.internet.mu.Lock()
	for i := range results {
		if stored, ok := e.internet.targets[results[i].ID]; ok {
			*stored = results[i]
		}
	}
	e.internet.mu.Unlock()

	verdict := aggregateInternet(results, time.Now().UTC())
	e.states.SetSourceReachable(verdict.Status != InternetDown)
	return verdict
}

// checkInternetTarget probes one connectivity target.
func (e *Engine) checkInternetTarget(ctx context.Context, t InternetTarget) InternetTarget {
	out := t
	if !t.Enabled {
		return out
	}
	target := model.ProbeTarget{
		ID:       t.ID,
		Name:     t.Name,
		Kind:     model.ProbeKindNetwork,
		Target:   t.Target,
		Port:     t.Port,
		Protocol: t.Protocol,
		SourceID: t.Source,
		Timeout:  internetTargetTimeout,
		Enabled:  true,
	}
	rec := e.executor.Execute(ctx, target)
	out.Reachable = rec.Result.Success
	out.LatencyMS = rec.Result.LatencyMS
	out.Status = rec.Result.Status
	out.ErrorType = rec.Result.ErrorType
	out.ErrorMessage = rec.Result.ErrorMessage
	out.LastCheckedAt = rec.Result.Timestamp
	out.Source = rec.SourceID
	return out
}

// internetTargetTimeout is the per-target timeout for connectivity checks. It is
// deliberately shorter than a device probe: the verdict must not wait for a
// black-holed host.
const internetTargetTimeout = 2

// aggregateInternet computes the verdict from per-target results.
//
// It is a pure function of its arguments so the API layer (and the tests) can
// evaluate the aggregation without a scheduler.
func aggregateInternet(targets []InternetTarget, checkedAt time.Time) InternetVerdict {
	v := InternetVerdict{
		Targets:   targets,
		Source:    DefaultSourceID,
		CheckedAt: checkedAt,
	}
	if len(targets) == 0 {
		v.Status = InternetUnknown
		v.Summary = "no internet connectivity targets configured"
		return v
	}

	byRole := map[string]*InternetGroup{}
	var order []string
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		g, ok := byRole[t.Role]
		if !ok {
			g = &InternetGroup{Role: t.Role}
			byRole[t.Role] = g
			order = append(order, t.Role)
		}
		g.Total++
		if t.Reachable {
			g.Reachable++
		}
		if t.Source != "" {
			v.Source = t.Source
		}
	}
	for _, role := range order {
		v.Groups = append(v.Groups, *byRole[role])
	}

	var (
		reachable int
		failed    []InternetTarget
	)
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		if t.Reachable {
			reachable++
		} else {
			failed = append(failed, t)
		}
	}

	switch {
	case reachable == 0:
		v.Status = InternetDown
		v.Summary = "no connectivity target is reachable from " + v.Source
	case len(failed) == 0:
		v.Status = InternetHealthy
		v.Summary = fmt.Sprintf("all %d connectivity targets are reachable from %s", reachable, v.Source)
	default:
		v.Status = InternetDegraded
		names := make([]string, 0, len(failed))
		for _, t := range failed {
			names = append(names, t.Name)
		}
		v.Summary = fmt.Sprintf("%d of %d targets unreachable from %s: %s",
			len(failed), reachable+len(failed), v.Source, strings.Join(names, ", "))
	}
	return v
}

// InternetVerdict returns the last computed connectivity verdict without
// running any probe. The API uses this for the Overview endpoint.
func (e *Engine) InternetVerdict() InternetVerdict {
	return aggregateInternet(e.InternetTargets(), time.Now().UTC())
}
