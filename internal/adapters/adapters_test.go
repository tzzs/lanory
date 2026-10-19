package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lanory/lanory/internal/discovery"
)

// errStub is a factory error used by the registry isolation test.
var errStub = errors.New("stub factory failure")

// stubAdapter is a minimal Adapter for registry tests.
type stubAdapter struct {
	name string
}

func (s stubAdapter) Name() string               { return s.name }
func (s stubAdapter) Manufacturer() string       { return "stub" }
func (s stubAdapter) Capabilities() []Capability { return nil }
func (s stubAdapter) Probe(ctx context.Context) ([]discovery.Result, error) {
	return nil, nil
}
func (s stubAdapter) Status() Status   { return Status{} }
func (s stubAdapter) Fallback() string { return FallbackPassive }

// TestCredentialRedaction is the security regression test for credential
// handling: no formatting verb and no serialiser may reveal the secret.
func TestCredentialRedaction(t *testing.T) {
	const secret = "s3cr3t-t0ken-do-not-leak"
	c := NewCredential(CredentialToken, "admin", secret)

	renderings := map[string]string{
		"%v":  strings.TrimSpace(sprint("%v", c)),
		"%s":  strings.TrimSpace(sprint("%s", c)),
		"%+v": strings.TrimSpace(sprint("%+v", c)),
		"%#v": strings.TrimSpace(sprint("%#v", c)),
		"%q":  strings.TrimSpace(sprint("%q", c)),
	}
	for verb, got := range renderings {
		if strings.Contains(got, secret) {
			t.Errorf("%s leaked the secret: %s", verb, got)
		}
		if !strings.Contains(got, "redacted") {
			t.Errorf("%s did not mark the secret as redacted: %s", verb, got)
		}
	}

	if got := c.String(); strings.Contains(got, secret) {
		t.Errorf("String() leaked the secret: %s", got)
	}
	if got := c.GoString(); strings.Contains(got, secret) {
		t.Errorf("GoString() leaked the secret: %s", got)
	}
	if got := c.Redacted(); strings.Contains(got, secret) {
		t.Errorf("Redacted() leaked the secret: %s", got)
	}

	// JSON serialisation, both directly and embedded in a struct.
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Errorf("json.Marshal leaked the secret: %s", b)
	}
	holder := struct {
		Credential Credential `json:"credential"`
	}{Credential: c}
	b, err = json.Marshal(holder)
	if err != nil {
		t.Fatalf("marshal holder: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Errorf("json.Marshal of an embedding struct leaked the secret: %s", b)
	}

	// The secret is still reachable through the one explicit accessor, otherwise
	// the adapter could not authenticate.
	if c.Secret() != secret {
		t.Error("Secret() did not return the configured secret")
	}
	if !c.Configured() {
		t.Error("Configured() = false for a credential with a secret")
	}
	if c.Kind() != CredentialToken {
		t.Errorf("Kind() = %q, want %q", c.Kind(), CredentialToken)
	}
	if c.Username() != "admin" {
		t.Errorf("Username() = %q", c.Username())
	}
}

// TestEmptySecretIsNotConfigured makes sure an empty password can never cause an
// authentication attempt with an empty string.
func TestEmptySecretIsNotConfigured(t *testing.T) {
	for _, secret := range []string{"", "   ", "\t\n"} {
		c := NewCredential(CredentialBasic, "root", secret)
		if c.Configured() {
			t.Errorf("credential with secret %q reports Configured() = true", secret)
		}
		if c.Kind() != CredentialNone {
			t.Errorf("credential with secret %q has kind %q, want none", secret, c.Kind())
		}
		if c.Secret() != "" {
			t.Errorf("credential with empty secret returns a non-empty Secret()")
		}
	}
}

// TestCredentialStore checks scoping and that a missing entry is a safe value.
func TestCredentialStore(t *testing.T) {
	s := NewCredentialStore()
	if got := s.Get("missing"); got.Configured() {
		t.Error("Get on an empty store returned a configured credential")
	}
	s.Set("xiaomi:living-room", NewCredential(CredentialToken, "", "abc"))
	s.Set("xiaomi:empty", NewCredential(CredentialToken, "", ""))

	ids := s.IDs()
	if len(ids) != 1 || ids[0] != "xiaomi:living-room" {
		t.Errorf("IDs() = %v, want [xiaomi:living-room]", ids)
	}
	if !s.Get("xiaomi:living-room").Configured() {
		t.Error("stored credential is not configured")
	}
	if s.Get("xiaomi:empty").Configured() {
		t.Error("empty credential is reported as configured")
	}
	s.Delete("xiaomi:living-room")
	if s.Get("xiaomi:living-room").Configured() {
		t.Error("Delete did not remove the credential")
	}
	s.Set("a", NewCredential(CredentialBasic, "u", "p"))
	s.Clear()
	if len(s.IDs()) != 0 {
		t.Error("Clear did not remove every credential")
	}
}

// TestCapabilitySet checks the set semantics, including the CapClients alias.
func TestCapabilitySet(t *testing.T) {
	set := CapabilitySet{CapWirelessClients, CapSSID}
	if !set.Has(CapWirelessClients) {
		t.Error("Has(CapWirelessClients) = false")
	}
	if !set.Has(CapClients) {
		t.Error("Has(CapClients) should match CapWirelessClients")
	}
	if set.Has(CapModel) {
		t.Error("Has(CapModel) = true for a set that does not contain it")
	}
	if got := set.Strings(); len(got) != 2 || got[0] != "wireless_clients" {
		t.Errorf("Strings() = %v", got)
	}

	// Sorted must not mutate the receiver.
	sorted := CapabilitySet{CapSSID, CapModel, CapBand}.Sorted()
	if sorted[0] != CapBand || sorted[1] != CapModel || sorted[2] != CapSSID {
		t.Errorf("Sorted() = %v", sorted)
	}
	orig := CapabilitySet{CapSSID, CapModel}
	_ = orig.Sorted()
	if orig[0] != CapSSID {
		t.Error("Sorted() mutated its receiver")
	}
}

// TestCapabilityValidity checks the registry of capability names.
func TestCapabilityValidity(t *testing.T) {
	for _, c := range AllCapabilities() {
		if !ValidCapability(c) {
			t.Errorf("%q is listed but not valid", c)
		}
		if Describe(c) == "unknown capability" {
			t.Errorf("%q has no description", c)
		}
	}
	if ValidCapability("teleport") {
		t.Error("ValidCapability accepted an invented capability")
	}
}

// TestConfigNormalize checks defaults and rejections.
func TestConfigNormalize(t *testing.T) {
	c, err := Config{Vendor: "xiaomi", Address: "192.168.100.2"}.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.ID != "xiaomi:192.168.100.2" {
		t.Errorf("default ID = %q", c.ID)
	}
	if c.Timeout != DefaultTimeout {
		t.Errorf("default timeout = %v", c.Timeout)
	}
	if c.OperatingMode != ModeUnknown {
		t.Errorf("default mode = %q", c.OperatingMode)
	}

	for _, bad := range []Config{
		{Address: "192.168.100.2"},                                    // no vendor
		{Vendor: "xiaomi"},                                            // no address
		{Vendor: "xiaomi", Address: "x", Port: 70000},                 // bad port
		{Vendor: "xiaomi", Address: "x", Timeout: 60 * 10 * 1e9},      // too long
		{Vendor: "xiaomi", Address: "x", OperatingMode: "teleporter"}, // bad mode
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("Normalize accepted invalid config %+v", bad)
		}
	}
}

// TestRegistryIsolatesVendors checks that one bad vendor does not affect another.
func TestRegistryIsolatesVendors(t *testing.T) {
	r := NewRegistry()
	r.Register("good", func(c Config) (Adapter, error) { return stubAdapter{name: "good"}, nil })
	r.Register("bad", func(c Config) (Adapter, error) { return nil, errStub })

	if _, err := r.Build(Config{Vendor: "bad", Address: "192.168.100.9"}); err == nil {
		t.Fatal("Build with the failing vendor returned nil error")
	}
	a, err := r.Build(Config{Vendor: "good", Address: "192.168.100.9"})
	if err != nil {
		t.Fatalf("Build with the healthy vendor failed after the other one failed: %v", err)
	}
	if a.Name() != "good" {
		t.Errorf("built adapter name = %q", a.Name())
	}
	if _, err := r.Build(Config{Vendor: "nonexistent", Address: "192.168.100.9"}); err == nil {
		t.Fatal("Build accepted an unknown vendor")
	}
	if got := r.Vendors(); len(got) != 2 {
		t.Errorf("Vendors() = %v", got)
	}
}

// TestResultSource checks the source mapping used to tag evidence.
func TestResultSource(t *testing.T) {
	if got := ResultSource("xiaomi"); got != "xiaomi" {
		t.Errorf("ResultSource(xiaomi) = %q", got)
	}
	if got := ResultSource("GENERIC"); got == "" {
		t.Error("ResultSource(generic) is empty")
	}
	if got := ResultSource(""); got == "" {
		t.Error("ResultSource(empty) is empty")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// sprint renders args with a format verb, so the table above reads as "render
// this credential with verb X".
func sprint(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
