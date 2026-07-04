package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// netConfigOf builds a Config carrying the given model. tfsdk.Config has no
// Set, so we Set into a State (which does) and reuse its Raw tftypes value.
func netConfigOf(t *testing.T, m networkModel) tfsdk.Config {
	t.Helper()
	s := netStateOf(t, m)
	return tfsdk.Config{Schema: s.Schema, Raw: s.Raw}
}

func validateNetworkConfig(t *testing.T, m networkModel) *resource.ValidateConfigResponse {
	t.Helper()
	r := NewNetworkResource().(*networkResource)
	var resp resource.ValidateConfigResponse
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: netConfigOf(t, m)}, &resp)
	return &resp
}

// gatewayBlock is a minimal valid gateway block (no DHCP) for config tests.
func gatewayBlock() *gatewayModel {
	return &gatewayModel{
		HostIPAddress:    types.StringValue("192.168.10.1"),
		PrefixLength:     types.Int64Value(24),
		AutoScaleEnabled: types.BoolValue(false),
	}
}

func TestNetworkValidateConfig(t *testing.T) {
	base := func() networkModel {
		return networkModel{Name: types.StringValue("n"), VlanID: types.Int64Value(10)}
	}

	t.Run("gateway management without block errors", func(t *testing.T) {
		m := base()
		m.Management = types.StringValue("GATEWAY")
		resp := validateNetworkConfig(t, m)
		if !netDiagsContain(resp.Diagnostics, "Missing gateway block") {
			t.Fatalf("want missing-block error, got: %v", resp.Diagnostics)
		}
	})

	t.Run("unmanaged with block errors", func(t *testing.T) {
		m := base()
		m.Management = types.StringValue("UNMANAGED")
		m.Gateway = gatewayBlock()
		resp := validateNetworkConfig(t, m)
		if !netDiagsContain(resp.Diagnostics, "Unexpected gateway block") {
			t.Fatalf("want unexpected-block error, got: %v", resp.Diagnostics)
		}
	})

	t.Run("null management (defaults unmanaged) with block errors", func(t *testing.T) {
		m := base()
		m.Management = types.StringNull()
		m.Gateway = gatewayBlock()
		resp := validateNetworkConfig(t, m)
		if !netDiagsContain(resp.Diagnostics, "Unexpected gateway block") {
			t.Fatalf("want unexpected-block error, got: %v", resp.Diagnostics)
		}
	})

	t.Run("gateway management with block is valid", func(t *testing.T) {
		m := base()
		m.Management = types.StringValue("GATEWAY")
		m.Gateway = gatewayBlock()
		resp := validateNetworkConfig(t, m)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("null management without block is valid", func(t *testing.T) {
		m := base()
		m.Management = types.StringNull()
		resp := validateNetworkConfig(t, m)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})
}

// TestExpandNetworkGatewayMinimalDHCP covers the false branches in the DHCP
// expand path: a dhcp block with only the required range, leaving dns_servers,
// domain_name, and lease_time_seconds null. Those optional keys must be absent.
func TestExpandNetworkGatewayMinimalDHCP(t *testing.T) {
	body, diags := expandNetwork(context.Background(), networkModel{
		Name:       types.StringValue("Corp"),
		VlanID:     types.Int64Value(10),
		Enabled:    types.BoolValue(true),
		Management: types.StringValue("GATEWAY"),
		Gateway: &gatewayModel{
			HostIPAddress: types.StringValue("192.168.10.1"),
			PrefixLength:  types.Int64Value(24),
			DHCP: &dhcpModel{
				RangeStart:       types.StringValue("192.168.10.100"),
				RangeStop:        types.StringValue("192.168.10.200"),
				DNSServers:       types.ListNull(types.StringType),
				DomainName:       types.StringNull(),
				LeaseTimeSeconds: types.Int64Null(),
			},
		},
	})
	if diags.HasError() {
		t.Fatalf("expand diags: %v", diags)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dhcp := got["ipv4Configuration"].(map[string]any)["dhcpConfiguration"].(map[string]any)
	if dhcp["mode"] != "SERVER" {
		t.Errorf("mode = %v, want SERVER", dhcp["mode"])
	}
	for _, absent := range []string{"dnsServerIpAddressesOverride", "domainName", "leaseTimeSeconds"} {
		if _, ok := dhcp[absent]; ok {
			t.Errorf("optional key %q should be absent when null, got %v", absent, dhcp[absent])
		}
	}
}
