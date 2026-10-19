// Package xiaomi implements the phase 2 Xiaomi router adapter.
//
// Behaviour, in the order it happens:
//
//  1. If no credential is configured, the adapter makes ZERO HTTP requests. It
//     reports that the management API needs a credential Lanory does not have,
//     and returns an empty result set with a nil error. It never tries a default
//     or common password, never tries an anonymous login, and never retries with
//     a different username. Tests assert the request counter stays at zero.
//
//  2. If a credential is configured, the adapter probes a small set of known
//     management endpoints, one request each, and inspects the *shape* of the
//     response to decide which capabilities the device actually offers. Two
//     routers of the same vendor do not expose the same API, so nothing is
//     assumed: a missing endpoint simply removes a capability.
//
//  3. When no endpoint answers, the adapter returns empty results and reports
//     the fallback path (OpenWrt FDB + DHCP + ARP/NDP). This is not an error:
//     most Xiaomi routers either run stock firmware with no public API or run
//     OpenWrt where the passive collectors are strictly better.
//
// Security rules (from the product spec, non negotiable):
//
//   - The credential is held in memory only, is never logged and never sent
//     anywhere except the configured target address.
//   - Lanory never attempts to bypass authentication or exploit the device. The
//     adapter only calls endpoints that a documented management API exposes and
//     stops at the first 401/403.
//   - Plain HTTP is refused when a secret is configured and the target is not
//     loopback, because that would put the secret on the wire in clear text.
package xiaomi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/adapters"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Name is the stable adapter name.
const Name = "xiaomi"

// Manufacturer is the vendor this adapter targets.
const Manufacturer = "Xiaomi"

// maxBodyBytes caps how much of a response the adapter reads. A router has
// little memory and no response needs to be larger than this.
const maxBodyBytes = 1 << 20 // 1 MiB

// Endpoints are the management paths probed, in order. They are the documented
// paths of the Xiaomi / MiWiFi web API; a device that does not expose them
// simply yields no capability.
//
// The adapter never refers to these as "the Xiaomi API": they are the paths this
// adapter knows how to read, tried one at a time, and a 404 is a normal answer.
var Endpoints = []struct {
	// Path is the request path.
	Path string
	// Capability is what a successful, well shaped response proves.
	Capability adapters.Capability
}{
	{"/cgi-bin/luci/api/xqsystem/status", adapters.CapOperatingMode},
	{"/cgi-bin/luci/api/xqnetwork/wan_info", adapters.CapUplink},
	{"/cgi-bin/luci/api/xqnetwork/wifi_detail_all", adapters.CapSSID},
	{"/cgi-bin/luci/api/xqnetwork/wifi_connect_devices", adapters.CapWirelessClients},
	{"/cgi-bin/luci/api/misystem/devicelist", adapters.CapConnectionType},
	{"/cgi-bin/luci/api/misystem/sys_info", adapters.CapModel},
}

// Adapter is the Xiaomi router adapter.
type Adapter struct {
	cfg    adapters.Config
	client *http.Client
	dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	mu       sync.Mutex
	status   adapters.Status
	requests int
	// stopped records the HTTP status that stopped the probe (401/403), so the
	// status message can explain it without another request.
	stopped int
}

// New builds a Xiaomi adapter.
func New(cfg adapters.Config) (adapters.Adapter, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if cfg.Vendor == "" {
		cfg.Vendor = Name
	}
	if cfg.Port == 0 {
		cfg.Port = 80
	}
	a := &Adapter{
		cfg:    cfg,
		dialer: (&net.Dialer{Timeout: cfg.Timeout}).DialContext,
	}
	a.client = &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:           func(ctx context.Context, network, addr string) (net.Conn, error) { return a.dialer(ctx, network, addr) },
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: cfg.Timeout,
			TLSHandshakeTimeout:   cfg.Timeout,
			TLSClientConfig: &tls.Config{
				// Router certificates are self signed and scoped to the LAN.
				InsecureSkipVerify: true, //nolint:gosec // self signed LAN certificate
				MinVersion:         tls.VersionTLS12,
			},
		},
	}
	a.status = adapters.Status{
		AdapterID:     cfg.ID,
		Vendor:        cfg.Vendor,
		Claimed:       a.Capabilities(),
		OperatingMode: cfg.OperatingMode,
		Reason:        "not probed yet",
	}
	return a, nil
}

// Name implements adapters.Adapter.
func (a *Adapter) Name() string { return Name }

// Manufacturer implements adapters.Adapter.
func (a *Adapter) Manufacturer() string { return Manufacturer }

// Capabilities implements adapters.Adapter.
//
// This is the set the adapter can offer *when the device answers with the
// expected shape*. Probe reports what was actually detected; callers must use
// Status().Detected, never Capabilities(), to decide what is available.
func (a *Adapter) Capabilities() []adapters.Capability {
	return []adapters.Capability{
		adapters.CapWirelessClients,
		adapters.CapWiredClients,
		adapters.CapSSID,
		adapters.CapBand,
		adapters.CapSignal,
		adapters.CapConnectionType,
		adapters.CapOnlineStatus,
		adapters.CapModel,
		adapters.CapFirmware,
		adapters.CapOperatingMode,
		adapters.CapUplink,
	}
}

// Fallback implements adapters.Adapter.
func (a *Adapter) Fallback() string { return adapters.FallbackPassive }

// Status implements adapters.Adapter.
func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.status
	s.Requests = a.requests
	return s
}

// Probe implements adapters.Adapter.
//
// The zero-credential path returns before any network access happens. That is
// the single most important property of this method: an unconfigured adapter
// must not touch the device at all.
func (a *Adapter) Probe(ctx context.Context) ([]discovery.Result, error) {
	start := time.Now()
	a.mu.Lock()
	a.requests = 0
	a.stopped = 0
	a.mu.Unlock()

	status := adapters.Status{
		AdapterID:     a.cfg.ID,
		Vendor:        a.cfg.Vendor,
		Claimed:       a.Capabilities(),
		OperatingMode: a.cfg.OperatingMode,
		CheckedAt:     start.UTC(),
	}

	// --- hard stop 1: no credential -------------------------------------
	//
	// There is no anonymous attempt and no default password attempt. If the user
	// has not configured credentials, Lanory cannot read the management API, and
	// it says so instead of guessing.
	if !a.cfg.Credential.Configured() {
		status.Reachable = false
		status.Authenticated = false
		status.Reason = "no credentials configured; not attempting authentication " +
			"(Lanory never tries default passwords). Falling back to " + adapters.FallbackPassive
		a.finish(status, start, nil)
		return nil, nil
	}

	// --- hard stop 2: secret would go over the wire in clear text --------
	host := a.cfg.TargetHost()
	if host == "" {
		err := errors.New("xiaomi adapter: empty management address")
		status.Error = err.Error()
		a.finish(status, start, err)
		return nil, err
	}
	if a.cfg.Port != 443 && !a.cfg.AllowInsecure && !isLoopback(host) {
		status.Reason = fmt.Sprintf(
			"refusing to send credentials over plain HTTP to %s:%d; set allow_insecure or use port 443",
			host, a.cfg.Port)
		a.finish(status, start, nil)
		return nil, nil
	}

	scheme := "http"
	if a.cfg.Port == 443 {
		scheme = "https"
	}

	var (
		detected    = adapters.CapabilitySet{}
		collected   []discovery.Result
		reachable   bool
		authorised  bool
		lastErr     error
		stopAtFirst = false
	)

	for _, ep := range Endpoints {
		if stopAtFirst {
			break
		}
		url := fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, fmt.Sprint(a.cfg.Port)), ep.Path)
		body, code, err := a.get(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		reachable = true
		switch code {
		case http.StatusOK:
			// A 200 with a shaped body proves the capability. A 200 with an HTML
			// login page does not.
			if caps, ok := capabilitiesFromResponse(ep.Capability, body); ok {
				authorised = true
				for _, c := range caps {
					if !detected.Has(c) {
						detected = append(detected, c)
					}
				}
				if rs := resultsFromResponse(a.cfg, ep.Path, ep.Capability, body); len(rs) > 0 {
					collected = append(collected, rs...)
				}
			}
		case http.StatusUnauthorized, http.StatusForbidden:
			// The credential was rejected. Stop: retrying with another password
			// is credential guessing and is forbidden.
			a.mu.Lock()
			a.stopped = code
			a.mu.Unlock()
			stopAtFirst = true
		default:
			// 404, 500, a redirect to a captive portal: this endpoint is not
			// available on this device. Move on to the next one.
		}
	}

	durationMS := int(time.Since(start).Milliseconds())
	status.DurationMS = durationMS
	status.Reachable = reachable
	status.Authenticated = authorised
	status.Detected = detected

	switch {
	case !reachable:
		reason := "management API did not answer"
		if lastErr != nil {
			reason = "management API unreachable: " + sanitizeErr(lastErr)
		}
		status.Reason = reason + "; falling back to " + adapters.FallbackPassive
	case a.stopped == http.StatusUnauthorized || a.stopped == http.StatusForbidden:
		status.Reason = fmt.Sprintf("management API rejected the configured credential (HTTP %d); "+
			"Lanory did not retry. Falling back to %s", a.stopped, adapters.FallbackPassive)
	case len(detected) == 0:
		status.Reason = "management API answered but exposed none of the endpoints this adapter " +
			"understands; falling back to " + adapters.FallbackPassive
	default:
		status.Reason = "detected " + strings.Join(detected.Strings(), ", ")
	}

	if m := modelFromResponse(collected); m != "" {
		status.Model = m
	}
	a.finish(status, start, nil)
	return collected, nil
}

// get performs one authenticated GET and returns the body and status code.
func (a *Adapter) get(ctx context.Context, url string) ([]byte, int, error) {
	a.mu.Lock()
	a.requests++
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Lanory/0.1 (+https://github.com/lanory/lanory)")
	req.Header.Set("Accept", "application/json")

	c := a.cfg.Credential
	switch c.Kind() {
	case adapters.CredentialBasic:
		req.SetBasicAuth(c.Username(), c.Secret())
	case adapters.CredentialToken:
		req.Header.Set("Authorization", "Bearer "+c.Secret())
	default:
		// A session credential is carried as a bearer token too; obtaining a
		// session cookie requires a login POST, which this adapter deliberately
		// does not perform without an explicit, user visible flow.
		req.Header.Set("Authorization", "Bearer "+c.Secret())
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// capabilitiesFromResponse decides which capabilities a response shape proves.
//
// The test is deliberately conservative: an HTML page (a login form, a captive
// portal, a firmware upgrade notice) proves nothing, and a JSON document without
// the expected key proves nothing either.
//
// A client list can prove more than one capability at once: when an entry
// carries an explicit `wireless` flag, the device can distinguish wired from
// wireless clients, so CapConnectionType comes with CapWirelessClients. Nothing
// is granted without that explicit field.
func capabilitiesFromResponse(want adapters.Capability, body []byte) (adapters.CapabilitySet, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	if len(doc) == 0 {
		return nil, false
	}
	switch want {
	case adapters.CapOperatingMode:
		if hasAnyKey(doc, "mode", "wan", "hardware") {
			return adapters.CapabilitySet{want}, true
		}
	case adapters.CapUplink:
		if hasAnyKey(doc, "wan", "link", "ifname", "proto") {
			return adapters.CapabilitySet{want}, true
		}
	case adapters.CapSSID:
		if hasAnyKey(doc, "ssid", "wifi") || hasNestedList(doc, "list") {
			return adapters.CapabilitySet{want}, true
		}
	case adapters.CapWirelessClients:
		if hasAnyKey(doc, "list", "clients", "devices") || hasNestedList(doc, "list") {
			caps := adapters.CapabilitySet{want, adapters.CapClients}
			if listHasWirelessFlag(doc) {
				caps = append(caps, adapters.CapConnectionType, adapters.CapWiredClients, adapters.CapSSID, adapters.CapBand, adapters.CapSignal)
			}
			return caps, true
		}
	case adapters.CapConnectionType:
		if hasAnyKey(doc, "list", "devices", "mac") || hasNestedList(doc, "list") {
			return adapters.CapabilitySet{want}, true
		}
	case adapters.CapModel:
		if hasAnyKey(doc, "hardware", "model", "romversion", "sys") {
			return adapters.CapabilitySet{want}, true
		}
	}
	return nil, false
}

// listHasWirelessFlag reports whether any client entry carries an explicit
// wireless / connection / type discriminator.
func listHasWirelessFlag(doc map[string]any) bool {
	list, ok := doc["list"].([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := entry["wireless"]; ok {
			return true
		}
		if _, ok := entry["connection"]; ok {
			return true
		}
		if _, ok := entry["type"]; ok {
			return true
		}
	}
	return false
}

// resultsFromResponse turns a shaped response into normalized observations.
//
// Only client list responses produce results; everything else contributes
// capability facts to the status. The adapter never invents an observation it
// cannot read.
func resultsFromResponse(cfg adapters.Config, path string, cap adapters.Capability, body []byte) []discovery.Result {
	if cap != adapters.CapWirelessClients && cap != adapters.CapConnectionType {
		return nil
	}
	var doc struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.List) == 0 {
		return nil
	}
	now := time.Now().UTC()
	out := make([]discovery.Result, 0, len(doc.List))
	for _, c := range doc.List {
		mac := stringField(c, "mac", "macaddr", "hwaddr")
		if mac == "" {
			continue
		}
		wireless := boolField(c, "wireless") || stringField(c, "connection") == "wireless" ||
			stringField(c, "type") == "wifi"
		attrs := map[string]string{
			"adapter":    Name,
			"adapter_id": cfg.ID,
			"endpoint":   path,
			"connection": "wired",
		}
		if wireless {
			attrs["connection"] = "wireless"
			if ssid := stringField(c, "ssid", "essid"); ssid != "" {
				attrs["ssid"] = ssid
			}
			if band := stringField(c, "band", "frequency"); band != "" {
				attrs["band"] = band
			}
			if v, ok := intField(c, "signal", "rssi", "signal_strength"); ok {
				attrs["signal_dbm"] = fmt.Sprint(v)
			}
		}
		if ip := stringField(c, "ip", "ipaddr", "address"); ip != "" {
			attrs["ip"] = ip
		}
		if host := stringField(c, "name", "hostname", "devname"); host != "" {
			attrs["hostname"] = host
		}
		if online, ok := boolFieldOK(c, "online"); ok {
			attrs["online"] = fmt.Sprint(online)
		}

		res := discovery.Result{
			Kind:       discovery.ResultGeneric,
			Source:     model.SourceXiaomi,
			ObservedAt: now,
			Attributes: attrs,
			Link: &discovery.LinkObservation{
				RemoteMAC:  mac,
				LocalIP:    cfg.Address,
				LinkType:   model.EdgeTypeWirelessAssoc,
				Confidence: discovery.ConfidenceLLDP,
				Extra:      attrs,
			},
		}
		if !wireless {
			// A wired client observed by the vendor API is still only a port
			// observation; it does not prove which port.
			res.Link.LinkType = model.EdgeTypeLogicalNetwork
			res.Link.Confidence = discovery.ConfidenceDHCP
		}
		out = append(out, res)
	}
	return out
}

// modelFromResponse extracts a model string, if the adapter saw one.
func modelFromResponse(results []discovery.Result) string {
	for _, r := range results {
		if m := r.Attributes["model"]; m != "" {
			return m
		}
	}
	return ""
}

// finish stores the status and records the final duration.
func (a *Adapter) finish(s adapters.Status, start time.Time, err error) {
	if s.CheckedAt.IsZero() {
		s.CheckedAt = start.UTC()
	}
	s.DurationMS = int(time.Since(start).Milliseconds())
	if err != nil {
		s.Error = err.Error()
	}
	a.mu.Lock()
	s.Requests = a.requests
	a.status = s
	a.mu.Unlock()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func hasAnyKey(doc map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := doc[k]; ok {
			return true
		}
	}
	return false
}

// hasNestedList reports whether a nested object carries a list of objects.
func hasNestedList(doc map[string]any, key string) bool {
	v, ok := doc[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return false
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func boolField(m map[string]any, keys ...string) bool {
	v, _ := boolFieldOK(m, keys...)
	return v
}

func boolFieldOK(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				return t, true
			case float64:
				return t != 0, true
			case string:
				return t == "1" || strings.EqualFold(t, "true") || strings.EqualFold(t, "online"), true
			}
		}
	}
	return false, false
}

func intField(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int(t), true
			case int:
				return t, true
			}
		}
	}
	return 0, false
}

// isLoopback reports whether host is a loopback address.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback()
}

// sanitizeErr strips the URL from a transport error.
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "://"); i >= 0 {
		if j := strings.Index(msg[i:], " "); j > 0 {
			return msg[:i] + "[url]" + msg[i+j:]
		}
	}
	return msg
}
