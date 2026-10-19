// Package adapters defines the extensible router adapter system.
//
// A router adapter is an *optional* extra source of topology information for a
// device that Lanory cannot fully see by itself. Lanory already collects the
// router's own view (interfaces, bridge FDB, ARP/NDP, DHCP leases); an adapter
// adds vendor specific facts such as "which wireless clients are associated to
// this AP and on which band", which the FDB alone cannot prove.
//
// Design rules, all of which are enforced by tests in this package:
//
//  1. An adapter never modifies the target device. Everything is read only.
//
//  2. An adapter declares its capabilities instead of assuming them. Two
//     routers of the same vendor may expose completely different APIs, so
//     Capabilities() reports what was actually detected, and callers must check
//     it before using a capability. A missing capability never fails the rest
//     of the application: the collector simply reports fewer facts.
//
//  3. Absence of data is not an error. Probe returns an empty result set and a
//     nil error when the management interface is unreachable, because that is
//     the normal state of a home network (most routers have no public API).
//     The caller decides how to fall back; see FallbackPassive.
//
//  4. Credentials are secrets. They are held in memory only (Credential.Store
//     is an in-memory holder, never persisted, never logged, never serialised),
//     redacted in every String/Format/JSON representation, and only ever sent
//     to the configured target address.
//
//  5. No credential guessing, ever. An adapter that has no configured
//     credentials makes zero authentication attempts and zero HTTP requests. It
//     never tries a default or common password, never retries with a different
//     username, and never attempts to bypass authentication or exploit the
//     device. If the management API needs a login Lanory does not have, the
//     adapter reports that fact and stops.
//
// The adapter registry is the extension point for future vendors (TP-Link,
// ASUS, Huawei, generic SNMP, generic LLDP...). Registering an adapter must not
// change the behaviour of any other adapter.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

// Capability names an optional fact an adapter may be able to provide.
//
// These are adapter level capabilities, deliberately separate from the
// collector capabilities in internal/discovery: a collector capability
// describes what a data source can read on the local host, an adapter capability
// describes what a vendor API can add about a remote device.
type Capability string

const (
	// CapWirelessClients means the adapter can enumerate associated wireless
	// clients with their MAC address.
	CapWirelessClients Capability = "wireless_clients"
	// CapWiredClients means the adapter can enumerate wired clients (its own
	// switch ports' learned MACs).
	CapWiredClients Capability = "wired_clients"
	// CapSSID means the adapter can report the SSID a client is associated to.
	CapSSID Capability = "ssid"
	// CapBand means the adapter can report the radio band (2.4/5/6 GHz).
	CapBand Capability = "band"
	// CapSignal means the adapter can report client signal strength.
	CapSignal Capability = "signal"
	// CapConnectionType means the adapter can distinguish wired from wireless.
	CapConnectionType Capability = "connection_type"
	// CapOnlineStatus means the adapter can report a per client online flag.
	CapOnlineStatus Capability = "online_status"
	// CapModel means the adapter can report the device model.
	CapModel Capability = "model"
	// CapFirmware means the adapter can report the firmware version.
	CapFirmware Capability = "firmware"
	// CapOperatingMode means the adapter can report its working mode (router,
	// access point, bridge, repeater), which decides whether its client list is
	// authoritative for the LAN.
	CapOperatingMode Capability = "operating_mode"
	// CapUplink means the adapter can report which port or radio it uses as
	// its uplink.
	CapUplink Capability = "uplink"
	// CapClients is the union used by callers that only care whether any client
	// enumeration works.
	CapClients Capability = "clients"
)

// AllCapabilities lists every capability in a stable order, for display.
func AllCapabilities() []Capability {
	return []Capability{
		CapWirelessClients, CapWiredClients, CapClients, CapSSID, CapBand,
		CapSignal, CapConnectionType, CapOnlineStatus, CapModel, CapFirmware,
		CapOperatingMode, CapUplink,
	}
}

// ValidCapability reports whether c is a known capability.
func ValidCapability(c Capability) bool {
	for _, v := range AllCapabilities() {
		if v == c {
			return true
		}
	}
	return false
}

// Describe returns a human readable explanation of a capability, for the UI.
func Describe(c Capability) string {
	switch c {
	case CapWirelessClients:
		return "enumerate associated wireless clients"
	case CapWiredClients:
		return "enumerate wired clients learned by the device"
	case CapClients:
		return "enumerate clients (wired or wireless)"
	case CapSSID:
		return "report the SSID a client uses"
	case CapBand:
		return "report the radio band"
	case CapSignal:
		return "report client signal strength"
	case CapConnectionType:
		return "distinguish wired from wireless clients"
	case CapOnlineStatus:
		return "report a per client online flag"
	case CapModel:
		return "report the device model"
	case CapFirmware:
		return "report the firmware version"
	case CapOperatingMode:
		return "report the operating mode (router, AP, bridge)"
	case CapUplink:
		return "report the uplink port or radio"
	}
	return "unknown capability"
}

// CapabilitySet is a set of capabilities with order-preserving operations.
type CapabilitySet []Capability

// Has reports whether the set contains c. CapClients matches either client
// enumeration capability so callers do not have to special case it.
func (s CapabilitySet) Has(c Capability) bool {
	for _, v := range s {
		if v == c {
			return true
		}
		if c == CapClients && (v == CapWirelessClients || v == CapWiredClients) {
			return true
		}
	}
	return false
}

// Strings renders the set for JSON responses and logs.
func (s CapabilitySet) Strings() []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, string(c))
	}
	return out
}

// Sorted returns a copy sorted alphabetically, for deterministic output.
func (s CapabilitySet) Sorted() CapabilitySet {
	out := append(CapabilitySet{}, s...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

// CredentialKind classifies how an adapter authenticates.
type CredentialKind string

const (
	// CredentialNone means the adapter needs no authentication (or none is
	// configured, in which case the adapter must make no attempt).
	CredentialNone CredentialKind = "none"
	// CredentialBasic is HTTP basic authentication with a username/password.
	CredentialBasic CredentialKind = "basic"
	// CredentialToken is a bearer / API token.
	CredentialToken CredentialKind = "token"
	// CredentialSession is a username/password login that yields a session
	// cookie or token.
	CredentialSession CredentialKind = "session"
)

// Credential is a read-only credential pair for one router.
//
// The struct is deliberately hostile to accidental disclosure:
//
//   - String, GoString, Format and MarshalJSON all redact the secret;
//   - the fields are unexported so they cannot be reached through reflection
//     based serialisers such as encoding/json or yaml;
//   - the only way to read the secret is Secret(), which callers must use at
//     the exact point of authentication.
//
// There is deliberately no method that returns the credential for logging.
type Credential struct {
	kind     CredentialKind
	username string
	secret   string
	// configured records whether a secret is present. A Credential with
	// configured == false must never cause an authentication attempt.
	configured bool
}

// NewCredential builds a credential. An empty secret yields a non-configured
// credential of kind CredentialNone, so "no credentials" and "empty credentials"
// behave identically and no adapter can accidentally try an empty password.
func NewCredential(kind CredentialKind, username, secret string) Credential {
	if strings.TrimSpace(secret) == "" {
		return Credential{kind: CredentialNone}
	}
	return Credential{kind: kind, username: username, secret: secret, configured: true}
}

// NoCredential returns a credential that is not configured.
func NoCredential() Credential { return Credential{kind: CredentialNone} }

// Kind returns the credential kind.
func (c Credential) Kind() CredentialKind {
	if c.kind == "" {
		return CredentialNone
	}
	return c.kind
}

// Configured reports whether a secret is present.
func (c Credential) Configured() bool { return c.configured }

// Username returns the username. It is not secret, and is safe to show in the
// UI so the user can confirm which account is configured.
func (c Credential) Username() string { return c.username }

// Secret returns the secret. Call it only when composing an authentication
// header for the configured target; never store, log or forward the result.
func (c Credential) Secret() string { return c.secret }

// Redacted renders a credential for diagnostics.
func (c Credential) Redacted() string {
	if !c.configured {
		return "<no credentials configured>"
	}
	user := c.username
	if user == "" {
		user = "<anonymous>"
	}
	return fmt.Sprintf("%s user=%s secret=<redacted>", c.Kind(), user)
}

// String implements fmt.Stringer with redaction, so `%v` never leaks a secret.
func (c Credential) String() string { return c.Redacted() }

// GoString implements fmt.GoStringer with redaction, so `%#v` never leaks.
func (c Credential) GoString() string { return "adapters.Credential{" + c.Redacted() + "}" }

// Format implements fmt.Formatter with redaction for every verb including %s
// and %+v.
func (c Credential) Format(f fmt.State, verb rune) {
	fmt.Fprint(f, c.Redacted())
}

// MarshalJSON redacts the credential so it can never be serialised by accident.
func (c Credential) MarshalJSON() ([]byte, error) {
	return []byte(`"` + c.Redacted() + `"`), nil
}

// CredentialStore holds the in-memory credentials for the registered adapters.
//
// It is intentionally process-local: the configuration layer may read a token
// from a file at startup, but the store never writes secrets to the database or
// to disk.
type CredentialStore struct {
	mu   sync.RWMutex
	byID map[string]Credential
}

// NewCredentialStore returns an empty store.
func NewCredentialStore() *CredentialStore {
	return &CredentialStore{byID: make(map[string]Credential)}
}

// Set stores the credential for an adapter instance.
func (s *CredentialStore) Set(adapterID string, c Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = make(map[string]Credential)
	}
	s.byID[adapterID] = c
}

// Get returns the credential for an adapter instance; a missing entry is a
// non-configured credential, never nil, so callers cannot panic.
func (s *CredentialStore) Get(adapterID string) Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.byID[adapterID]; ok {
		return c
	}
	return NoCredential()
}

// Delete removes the credential for an adapter instance.
func (s *CredentialStore) Delete(adapterID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, adapterID)
}

// Clear removes every credential.
func (s *CredentialStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = make(map[string]Credential)
}

// IDs lists the adapter IDs with a configured credential.
func (s *CredentialStore) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.byID))
	for id, c := range s.byID {
		if c.Configured() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// OperatingMode is the working mode the user says the device runs in.
//
// The adapter does not assume it: in AP/bridge mode the client list is
// authoritative for what is behind the device; in router mode it is not (NAT
// hides the clients), so the topology engine must keep the device as a leaf.
type OperatingMode string

const (
	// ModeUnknown means the mode has not been determined.
	ModeUnknown OperatingMode = "unknown"
	// ModeRouter means the device routes and NATs.
	ModeRouter OperatingMode = "router"
	// ModeAP means the device bridges, so its client list is authoritative.
	ModeAP OperatingMode = "ap"
	// ModeBridge means the device bridges without wireless.
	ModeBridge OperatingMode = "bridge"
	// ModeRepeater means the device repeats a wireless uplink.
	ModeRepeater OperatingMode = "repeater"
)

// Config configures one adapter instance.
type Config struct {
	// ID identifies the adapter instance, e.g. "xiaomi-living-room". It scopes
	// credentials and log lines.
	ID string
	// Vendor selects the adapter implementation ("generic", "xiaomi").
	Vendor string
	// Name is the display name of the device.
	Name string
	// Address is the management address. Only this address is ever contacted.
	Address string
	// Port is the management port; zero means the adapter default.
	Port int
	// Credential is the authentication secret, held in memory only.
	Credential Credential
	// OperatingMode is what the user says the device does.
	OperatingMode OperatingMode
	// Timeout bounds a single probe. Zero means DefaultTimeout.
	Timeout time.Duration
	// AllowInsecure permits plain HTTP instead of HTTPS. It defaults to false,
	// and an adapter must never silently send a secret over plain HTTP to a
	// non-loopback address.
	AllowInsecure bool
}

// DefaultTimeout is the per-probe timeout when Config.Timeout is zero.
const DefaultTimeout = 5 * time.Second

// Normalize fills in defaults and validates the configuration.
func (c Config) Normalize() (Config, error) {
	c.ID = strings.TrimSpace(c.ID)
	c.Vendor = strings.ToLower(strings.TrimSpace(c.Vendor))
	c.Name = strings.TrimSpace(c.Name)
	c.Address = strings.TrimSpace(c.Address)
	if c.ID == "" {
		c.ID = c.Vendor + ":" + c.Address
	}
	if c.Vendor == "" {
		return c, errors.New("adapters: vendor is required")
	}
	if c.Address == "" {
		return c, errors.New("adapters: address is required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return c, fmt.Errorf("adapters: invalid port %d", c.Port)
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Timeout > 60*time.Second {
		return c, errors.New("adapters: timeout must be at most 60s")
	}
	if c.OperatingMode == "" {
		c.OperatingMode = ModeUnknown
	}
	switch c.OperatingMode {
	case ModeUnknown, ModeRouter, ModeAP, ModeBridge, ModeRepeater:
	default:
		return c, fmt.Errorf("adapters: invalid operating mode %q", c.OperatingMode)
	}
	return c, nil
}

// TargetHost returns the host part of Address, which may include a port.
func (c Config) TargetHost() string {
	host, _, err := net.SplitHostPort(c.Address)
	if err == nil {
		return host
	}
	return strings.Trim(c.Address, "[]")
}

// TargetAddrPort returns the normalised address and port the adapter may
// contact. It refuses hostnames that are not literal IP addresses only when the
// caller asked for that; adapters resolve through the injected dialer.
func (c Config) TargetAddrPort() (netip.AddrPort, bool) {
	addr := c.Address
	if c.Port > 0 && !strings.Contains(addr, ":") {
		return netip.AddrPort{}, false
	}
	if ap, err := netip.ParseAddrPort(addr); err == nil {
		return ap, true
	}
	if ip, err := netip.ParseAddr(addr); err == nil {
		port := c.Port
		if port == 0 {
			port = 80
		}
		return netip.AddrPortFrom(ip, uint16(port)), true
	}
	return netip.AddrPort{}, false
}

// ---------------------------------------------------------------------------
// Adapter interface
// ---------------------------------------------------------------------------

// Status is the result of an adapter probe, including the reason nothing was
// returned. It is what the settings page shows, so a user can see why an
// adapter is idle instead of guessing.
type Status struct {
	// AdapterID identifies the adapter instance.
	AdapterID string `json:"adapter_id"`
	// Vendor is the adapter implementation.
	Vendor string `json:"vendor"`
	// Reachable reports whether the management endpoint answered.
	Reachable bool `json:"reachable"`
	// Authenticated reports whether the adapter has a working credential. It is
	// false whenever no credential is configured, even if the endpoint would
	// accept anonymous requests.
	Authenticated bool `json:"authenticated"`
	// Detected lists the capabilities that were actually observed, not the ones
	// the vendor might support.
	Detected CapabilitySet `json:"-"`
	// Claimed lists the capabilities the adapter can offer when the endpoint
	// answers with the expected shape.
	Claimed CapabilitySet `json:"-"`
	// OperatingMode is the mode the adapter observed or was configured with.
	OperatingMode OperatingMode `json:"operating_mode"`
	// Model and Firmware are reported when CapModel / CapFirmware are detected.
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	// Reason explains an empty result in human readable form.
	Reason string `json:"reason,omitempty"`
	// Error is only set when the probe failed in a way the user must fix
	// (malformed configuration, TLS failure). An unreachable device is not an
	// error.
	Error string `json:"error,omitempty"`
	// CheckedAt is when the probe ran.
	CheckedAt time.Time `json:"checked_at"`
	// DurationMS is how long the probe took.
	DurationMS int `json:"duration_ms"`
	// Requests is the number of HTTP requests the probe made. It is recorded so
	// tests (and the UI) can prove that a credential-less adapter made none.
	Requests int `json:"requests"`
}

// Capabilities returns the detected capabilities as strings.
func (s Status) Capabilities() []string { return s.Detected.Strings() }

// Adapter is the interface every router adapter implements.
//
// Implementations must be safe for concurrent use: Probe may be called while a
// previous call is still running, and the registry may hold several adapters of
// the same vendor.
type Adapter interface {
	// Name returns the stable adapter implementation name ("generic", "xiaomi").
	Name() string
	// Manufacturer returns the vendor name the adapter targets.
	Manufacturer() string
	// Capabilities returns the capabilities the adapter claims it can provide
	// when the endpoint responds as expected.
	Capabilities() []Capability
	// Probe reads the device and returns normalised discovery results.
	//
	// A device that cannot be reached yields an empty slice and a nil error.
	// Only a configuration problem the user must fix is returned as an error.
	Probe(ctx context.Context) ([]discovery.Result, error)
	// Status returns the outcome of the most recent Probe (or a fresh probe
	// result when Probe has not run yet, depending on the implementation).
	Status() Status
	// Fallback describes what Lanory does when this adapter contributes
	// nothing, so the UI can explain the degraded picture honestly.
	Fallback() string
}

// FallbackPassive is the fallback path shared by every adapter: read the
// router's own view instead of asking the vendor API.
const FallbackPassive = "OpenWrt bridge FDB + DHCP leases + ARP/NDP"

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Factory builds an adapter from a config.
type Factory func(Config) (Adapter, error)

// Registry maps a vendor name to its factory. A vendor that is registered but
// whose factory returns an error does not affect any other vendor.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	instances map[string]Adapter
	order     []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		instances: make(map[string]Adapter),
	}
}

// Register adds a vendor factory. Re-registering a vendor replaces it, which is
// what tests want.
func (r *Registry) Register(vendor string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	if vendor == "" || f == nil {
		return
	}
	if _, exists := r.factories[vendor]; !exists {
		r.order = append(r.order, vendor)
	}
	r.factories[vendor] = f
}

// Vendors lists the registered vendors in registration order.
func (r *Registry) Vendors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string{}, r.order...)
}

// Build instantiates an adapter. An unknown vendor is a user error (the user
// named a vendor Lanory does not support), so it is reported; a failing factory
// for one vendor is isolated to that vendor.
func (r *Registry) Build(cfg Config) (Adapter, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	f, ok := r.factories[cfg.Vendor]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("adapters: unsupported vendor %q (known: %s)",
			cfg.Vendor, strings.Join(r.Vendors(), ", "))
	}
	a, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("adapters: build %s adapter %q: %w", cfg.Vendor, cfg.ID, err)
	}
	r.mu.Lock()
	r.instances[cfg.ID] = a
	r.mu.Unlock()
	return a, nil
}

// Instances returns the built adapters in build order.
func (r *Registry) Instances() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.instances))
	for _, a := range r.instances {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ResultSource is the discovery source an adapter's results carry.
func ResultSource(vendor string) model.DiscoverySource {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "xiaomi":
		return model.SourceXiaomi
	case "generic":
		return model.SourceOpenWRT
	default:
		return model.SourceScan
	}
}
