// Package generic implements the phase 1 Generic Router adapter.
//
// This adapter exists for the routers that have no vendor management API at
// all, which is most of them. It offers no vendor specific facts: what it does
// is confirm that a management endpoint is present and reachable, and report the
// device as a node so the topology can draw it. Everything else (interfaces,
// clients, addresses) continues to come from the passive collectors.
//
// The important property is that it degrades to "no data" without erroring. A
// closed port, a timeout, a captive portal and an HTML login page are all normal
// states of a home router; none of them is a failure the user must fix, and none
// of them may stop the rest of the application.
package generic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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
const Name = "generic"

// DefaultPorts are the management ports probed in order. They are all plain HTTP
// or HTTPS on standard ports; a router that answers on none of them is treated
// as having no management interface, not as an error.
var DefaultPorts = []int{80, 443}

// Adapter is the generic router adapter.
type Adapter struct {
	cfg adapters.Config
	// client is used for every request. It is bounded by cfg.Timeout.
	http_ *http.Client
	// dialer lets a test substitute a fake device without touching the network.
	dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	mu     sync.Mutex
	status adapters.Status
	// requests counts HTTP requests made since construction. Tests assert it
	// stays zero when no credential is configured.
	requests int
}

// New builds a generic adapter.
func New(cfg adapters.Config) (adapters.Adapter, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if cfg.Vendor == "" {
		cfg.Vendor = Name
	}
	a := &Adapter{
		cfg:    cfg,
		dialer: (&net.Dialer{Timeout: cfg.Timeout}).DialContext,
	}
	a.http_ = &http.Client{
		Timeout: cfg.Timeout,
		// Redirects are followed at most once: a router that redirects to a
		// login page is enough to prove it exists.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 1 {
				return http.ErrUseLastResponse
			}
			return nil
		},
		Transport: &http.Transport{
			DialContext:           func(ctx context.Context, network, addr string) (net.Conn, error) { return a.dialer(ctx, network, addr) },
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: cfg.Timeout,
			TLSHandshakeTimeout:   cfg.Timeout,
			// The adapter talks to private LAN addresses, which never have a
			// certificate matching a public name. Chain verification is
			// therefore delegated to TLSConfig below; the adapter still never
			// sends a secret over a plain connection unless the user allowed it.
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
		Detected:      nil,
		Claimed:       a.Capabilities(),
		OperatingMode: cfg.OperatingMode,
		Reason:        "not probed yet",
	}
	return a, nil
}

// Name implements adapters.Adapter.
func (a *Adapter) Name() string { return Name }

// Manufacturer implements adapters.Adapter.
func (a *Adapter) Manufacturer() string { return "Generic" }

// Capabilities implements adapters.Adapter.
//
// The generic adapter claims almost nothing on purpose: without a vendor API it
// cannot enumerate clients, so pretending otherwise would be dishonest and would
// make the UI show empty client lists where it should show "not supported".
func (a *Adapter) Capabilities() []adapters.Capability {
	return []adapters.Capability{adapters.CapModel}
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
// It performs at most one request per candidate port and never authenticates
// unless a credential is configured. A device that answers is reported as a
// node; a device that does not is reported with a reason and produces an empty
// result set with a nil error.
func (a *Adapter) Probe(ctx context.Context) ([]discovery.Result, error) {
	start := time.Now()
	a.mu.Lock()
	a.requests = 0
	a.mu.Unlock()

	status := adapters.Status{
		AdapterID:     a.cfg.ID,
		Vendor:        a.cfg.Vendor,
		Claimed:       a.Capabilities(),
		OperatingMode: a.cfg.OperatingMode,
		CheckedAt:     start.UTC(),
	}

	host := a.cfg.TargetHost()
	if host == "" {
		a.finish(status, start, "no management address configured", errors.New("generic adapter: empty address"))
		return nil, errors.New("generic adapter: empty management address")
	}

	ports := DefaultPorts
	if a.cfg.Port > 0 {
		ports = []int{a.cfg.Port}
	}

	var (
		lastErr  error
		answered bool
		scheme   string
	)
	for _, port := range ports {
		for _, s := range a.schemes(port) {
			url := fmt.Sprintf("%s://%s/", s, net.JoinHostPort(host, fmt.Sprint(port)))
			reachable, err := a.head(ctx, url)
			if reachable {
				answered = true
				scheme = s
				break
			}
			if err != nil {
				lastErr = err
			}
		}
		if answered {
			break
		}
	}

	durationMS := int(time.Since(start).Milliseconds())
	status.DurationMS = durationMS

	if !answered {
		// Not an error: a router with no management interface is the expected
		// case, and the passive collectors already cover it.
		reason := "management endpoint did not answer"
		if lastErr != nil {
			reason = "management endpoint unreachable: " + sanitizeErr(lastErr)
		}
		status.Reachable = false
		status.Reason = reason + "; falling back to " + adapters.FallbackPassive
		a.finish(status, start, status.Reason, nil)
		return nil, nil
	}

	status.Reachable = true
	status.Reason = "management endpoint answered over " + strings.ToUpper(scheme)
	// The generic adapter cannot verify identity, so it does not claim any
	// capability it cannot prove. CapModel stays claimed, never detected.
	a.finish(status, start, status.Reason, nil)

	now := time.Now().UTC()
	results := []discovery.Result{
		{
			Kind:       discovery.ResultGeneric,
			Source:     model.SourceOpenWRT,
			ObservedAt: now,
			Attributes: map[string]string{
				"adapter":        Name,
				"adapter_id":     a.cfg.ID,
				"management_url": scheme + "://" + net.JoinHostPort(host, fmt.Sprint(portOf(a.cfg, scheme))),
				"reachable":      "true",
				"capabilities":   strings.Join(a.Status().Detected.Strings(), ","),
			},
			Link: &discovery.LinkObservation{
				RemoteIP:   host,
				LinkType:   model.EdgeTypeLogicalNetwork,
				Confidence: discovery.ConfidenceSubnet,
				Extra: map[string]string{
					"adapter": Name,
					"note":    "management endpoint reachable; relation not asserted",
				},
			},
		},
	}
	return results, nil
}

// schemes returns the URL schemes to try, HTTPS first. An insecure (plain HTTP)
// attempt is skipped when a secret is configured and the target is not
// loopback, because that would put the credential on the wire in clear text.
func (a *Adapter) schemes(port int) []string {
	if a.cfg.Credential.Configured() && !isLoopback(a.cfg.TargetHost()) {
		return []string{"https"}
	}
	if port == 443 {
		return []string{"https"}
	}
	return []string{"http", "https"}
}

// head issues one HEAD request and reports whether anything answered.
//
// A response of any status counts as "answered": a 401 or a 302 to a login page
// proves the device is there. Only a transport error counts as unreachable.
func (a *Adapter) head(ctx context.Context, url string) (bool, error) {
	a.mu.Lock()
	a.requests++
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "Lanory/0.1 (+https://github.com/lanory/lanory)")
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")

	// A credential is only attached when one is configured. There is no fallback
	// to an anonymous retry afterwards: if the credential is rejected, the
	// adapter reports that instead of trying to guess another one.
	if c := a.cfg.Credential; c.Configured() && c.Kind() == adapters.CredentialBasic {
		req.SetBasicAuth(c.Username(), c.Secret())
	}
	if c := a.cfg.Credential; c.Configured() && c.Kind() == adapters.CredentialToken {
		req.Header.Set("Authorization", "Bearer "+c.Secret())
	}

	resp, err := a.http_.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return true, nil
}

// finish stores the status.
func (a *Adapter) finish(s adapters.Status, start time.Time, reason string, err error) {
	if s.CheckedAt.IsZero() {
		s.CheckedAt = start.UTC()
	}
	if s.DurationMS == 0 {
		s.DurationMS = int(time.Since(start).Milliseconds())
	}
	if reason != "" && s.Reason == "" {
		s.Reason = reason
	}
	if err != nil {
		s.Error = err.Error()
	}
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

// portOf returns the port used for a scheme, for display only.
func portOf(c adapters.Config, scheme string) int {
	if c.Port > 0 {
		return c.Port
	}
	if scheme == "https" {
		return 443
	}
	return 80
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

// sanitizeErr strips the URL from a transport error so a log line cannot carry
// a token that a caller put in the query string. The error text is preserved.
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
