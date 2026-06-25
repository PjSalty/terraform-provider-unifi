package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// marshalDNSPolicy expands the model and returns the emitted JSON as a generic
// map, failing the test on any diagnostic or marshal error.
func marshalDNSPolicy(t *testing.T, m dnsPolicyModel) map[string]any {
	t.Helper()
	body, diags := expandDNSPolicy(m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// TestExpandDNSPolicyARecord proves the union-override path: the variant fields
// (domain, ipv4Address, ttlSeconds) ride the union while the named `enabled` and
// `type` survive MarshalJSON and carry the right values.
func TestExpandDNSPolicyARecord(t *testing.T) {
	got := marshalDNSPolicy(t, dnsPolicyModel{
		Type:        types.StringValue("A_RECORD"),
		Enabled:     types.BoolValue(true),
		Domain:      types.StringValue("host.example.com"),
		IPv4Address: types.StringValue("10.10.20.5"),
		TTLSeconds:  types.Int64Value(3600),
	})

	if got["type"] != "A_RECORD" {
		t.Errorf("type = %v, want A_RECORD", got["type"])
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["domain"] != "host.example.com" {
		t.Errorf("domain = %v, want host.example.com", got["domain"])
	}
	if got["ipv4Address"] != "10.10.20.5" {
		t.Errorf("ipv4Address = %v, want 10.10.20.5", got["ipv4Address"])
	}
	if got["ttlSeconds"] != float64(3600) {
		t.Errorf("ttlSeconds = %v, want 3600", got["ttlSeconds"])
	}
}

// TestExpandDNSPolicyEnabledOverride guards the gotcha specifically: even when
// the variant defaults Enabled to false, the named field must reflect the model.
func TestExpandDNSPolicyEnabledOverride(t *testing.T) {
	got := marshalDNSPolicy(t, dnsPolicyModel{
		Type:        types.StringValue("A_RECORD"),
		Enabled:     types.BoolValue(false),
		Domain:      types.StringValue("off.example.com"),
		IPv4Address: types.StringValue("10.0.0.1"),
	})
	if got["enabled"] != false {
		t.Errorf("enabled = %v, want false", got["enabled"])
	}
	// Omitted optional must not be emitted.
	if _, present := got["ttlSeconds"]; present {
		t.Errorf("ttlSeconds present, want omitted")
	}
}

// TestExpandDNSPolicySRV covers a variant whose fields exist ONLY on the union,
// confirming the dispatch reaches the SRV variant and all fields land.
func TestExpandDNSPolicySRV(t *testing.T) {
	got := marshalDNSPolicy(t, dnsPolicyModel{
		Type:         types.StringValue("SRV_RECORD"),
		Enabled:      types.BoolValue(true),
		Domain:       types.StringValue("_sip._tcp.example.com"),
		Port:         types.Int64Value(5060),
		Priority:     types.Int64Value(10),
		Protocol:     types.StringValue("tcp"),
		ServerDomain: types.StringValue("sip.example.com"),
		Service:      types.StringValue("sip"),
		Weight:       types.Int64Value(5),
	})

	if got["type"] != "SRV_RECORD" {
		t.Errorf("type = %v, want SRV_RECORD", got["type"])
	}
	if got["port"] != float64(5060) {
		t.Errorf("port = %v, want 5060", got["port"])
	}
	if got["priority"] != float64(10) {
		t.Errorf("priority = %v, want 10", got["priority"])
	}
	if got["serverDomain"] != "sip.example.com" {
		t.Errorf("serverDomain = %v, want sip.example.com", got["serverDomain"])
	}
	if got["weight"] != float64(5) {
		t.Errorf("weight = %v, want 5", got["weight"])
	}
}

// TestExpandDNSPolicyForwardDomain covers the non-record forwarding variant.
func TestExpandDNSPolicyForwardDomain(t *testing.T) {
	got := marshalDNSPolicy(t, dnsPolicyModel{
		Type:      types.StringValue("FORWARD_DOMAIN"),
		Enabled:   types.BoolValue(true),
		Domain:    types.StringValue("internal.example.com"),
		IPAddress: types.StringValue("10.10.20.13"),
	})

	if got["type"] != "FORWARD_DOMAIN" {
		t.Errorf("type = %v, want FORWARD_DOMAIN", got["type"])
	}
	if got["ipAddress"] != "10.10.20.13" {
		t.Errorf("ipAddress = %v, want 10.10.20.13", got["ipAddress"])
	}
}

// TestExpandDNSPolicyUnknownType ensures an unsupported type is a diagnostic,
// not a silent empty body.
func TestExpandDNSPolicyUnknownType(t *testing.T) {
	_, diags := expandDNSPolicy(dnsPolicyModel{
		Type:    types.StringValue("BOGUS"),
		Enabled: types.BoolValue(true),
	})
	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic for unknown type, got none")
	}
}
