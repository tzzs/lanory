package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// TestMonitoringStatusRecentIsAlwaysAnArray locks the wire contract for a probe
// that has never run.
//
// Recent is a nil Go slice when a probe has no results, and encoding/json
// marshals nil as null. The frontend reads recent.length on every probe, so a
// null there throws during render and React unmounts the whole app: the
// monitoring page went permanently blank on a fresh install, which is exactly
// the state where every probe has never run. The handler normalises it to [].
func TestMonitoringStatusRecentIsAlwaysAnArray(t *testing.T) {
	app := newTestApp(t)

	// A probe with no results at all: the state every probe starts in.
	//
	// The probe is attached to a real device because probe_targets.device_id
	// carries a foreign key to devices(id).
	dev := &model.Device{
		ID: "dev-probe-owner", Name: "owner", Type: model.DeviceTypeUnknown,
		State: model.StateUnknown,
	}
	if err := app.store.UpsertDevice(dev); err != nil {
		t.Fatalf("create device: %v", err)
	}
	fresh := &model.ProbeTarget{
		Name: "never run", Kind: model.ProbeKindNetwork, Target: "192.0.2.116",
		Protocol: model.ProbeICMP, Interval: 60, Timeout: 3, Enabled: true,
		DeviceID: dev.ID, FailureThreshold: 3, RecoverConfirm: 1, State: model.StateUnknown,
	}
	if err := app.store.CreateProbe(fresh); err != nil {
		t.Fatalf("create probe: %v", err)
	}

	// Decode into raw JSON so the test sees what the wire actually carries,
	// not what a typed decode would coerce.
	rec := do(t, app, http.MethodGet, "/api/v1/monitoring/status", "")
	assertStatus(t, rec, http.StatusOK)

	var raw struct {
		Probes []struct {
			Probe  model.ProbeTarget `json:"probe"`
			Recent json.RawMessage   `json:"recent"`
		} `json:"probes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode status: %v\nbody: %s", err, rec.Body.String())
	}

	var seen int
	for _, p := range raw.Probes {
		if p.Probe.ID != fresh.ID {
			continue
		}
		seen++
		if string(p.Recent) == "null" {
			t.Fatalf("recent for a probe that never ran is null, want []")
		}
		if string(p.Recent) != "[]" {
			t.Fatalf("recent = %s, want []", p.Recent)
		}
	}
	if seen != 1 {
		t.Fatalf("fresh probe appears %d times in status, want 1", seen)
	}

	// Every probe must carry an array, not just the fresh one.
	for _, p := range raw.Probes {
		if string(p.Recent) == "null" {
			t.Errorf("probe %q has recent=null, want []", p.Probe.Name)
		}
	}
}

// TestDeviceDiscoverySourcesIsAlwaysAnArray locks the same contract for a
// device's source list, which two components read with .map and .length.
func TestDeviceDiscoverySourcesIsAlwaysAnArray(t *testing.T) {
	app := newTestApp(t)

	// A device with no device_sources rows: UpsertDevice does not add one.
	dev := &model.Device{
		ID: "dev-no-sources", Name: "no sources", Type: model.DeviceTypeUnknown,
		State: model.StateUnknown,
	}
	if err := app.store.UpsertDevice(dev); err != nil {
		t.Fatalf("create device: %v", err)
	}

	for _, path := range []string{
		"/api/v1/devices?limit=200",
		"/api/v1/devices/dev-no-sources",
		"/api/v1/topology",
	} {
		rec := do(t, app, http.MethodGet, path, "")
		assertStatus(t, rec, http.StatusOK)
		body := rec.Body.String()

		var walk any
		if err := json.Unmarshal(rec.Body.Bytes(), &walk); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		var bad []string
		findNullArrays(walk, "discovery_sources", &bad)
		if len(bad) > 0 {
			t.Errorf("%s: discovery_sources is null at %v; want []", path, bad)
		}
		_ = body
	}
}

// findNullArrays walks a decoded JSON value and records the JSON paths where the
// named field is null.
func findNullArrays(v any, field string, out *[]string) {
	walkJSON(v, "", func(path string, obj map[string]any) {
		if raw, ok := obj[field]; ok && raw == nil {
			*out = append(*out, path+"."+field)
		}
	})
}

// walkJSON calls fn for every JSON object in the tree.
func walkJSON(v any, path string, fn func(path string, obj map[string]any)) {
	switch node := v.(type) {
	case map[string]any:
		fn(path, node)
		for k, child := range node {
			walkJSON(child, path+"."+k, fn)
		}
	case []any:
		for i, child := range node {
			walkJSON(child, path+"[]", fn)
			_ = i
		}
	}
}
