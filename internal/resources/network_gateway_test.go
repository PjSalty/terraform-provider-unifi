package resources

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

func firewallZoneSequence(zones []official.FirewallZone, sequenceErr error) iter.Seq2[official.FirewallZone, error] {
	return func(yield func(official.FirewallZone, error) bool) {
		for _, zone := range zones {
			if !yield(zone, nil) {
				return
			}
		}
		if sequenceErr != nil {
			var zero official.FirewallZone
			yield(zero, sequenceErr)
		}
	}
}

func TestResolveGatewayZoneID(t *testing.T) {
	internalID := uuid.New()
	newResource := func(zones []official.FirewallZone, sequenceErr error) *networkResource {
		client := &official.ClientMock{
			FirewallFunc: func() official.FirewallClient {
				return &official.FirewallClientMock{
					ListZonesAllFunc: func(context.Context, uuid.UUID, string) iter.Seq2[official.FirewallZone, error] {
						return firewallZoneSequence(zones, sequenceErr)
					},
				}
			},
		}
		return &networkResource{data: testutil.Data(client)}
	}
	base := func() networkModel {
		return networkModel{Management: types.StringValue(mgmtGateway), Gateway: gatewayBlock()}
	}

	t.Run("omitted zone resolves system Internal", func(t *testing.T) {
		m := base()
		var diags diag.Diagnostics
		newResource([]official.FirewallZone{
			{Id: uuid.New(), Name: "Custom", Metadata: official.UserOrSystemDefinedEntityMetadata{Origin: "USER_DEFINED"}},
			{Id: internalID, Name: "Internal", Metadata: official.UserOrSystemDefinedEntityMetadata{Origin: "SYSTEM_DEFINED"}},
		}, nil).resolveGatewayZoneID(context.Background(), &m, &diags)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if m.Gateway.ZoneID.ValueString() != internalID.String() {
			t.Errorf("zone_id = %q, want %q", m.Gateway.ZoneID.ValueString(), internalID)
		}
		body := marshalToMap(t, expandNetworkForTest(t, m))
		if body["zoneId"] != internalID.String() {
			t.Errorf("marshaled zoneId = %v, want %q", body["zoneId"], internalID)
		}
	})

	t.Run("explicit zone wins without lookup", func(t *testing.T) {
		m := base()
		explicitID := uuid.New()
		m.Gateway.ZoneID = types.StringValue(explicitID.String())
		var diags diag.Diagnostics
		(&networkResource{}).resolveGatewayZoneID(context.Background(), &m, &diags)
		if diags.HasError() || m.Gateway.ZoneID.ValueString() != explicitID.String() {
			t.Fatalf("explicit zone was not preserved: zone=%q diagnostics=%v", m.Gateway.ZoneID.ValueString(), diags)
		}
	})

	t.Run("lookup error is diagnostic", func(t *testing.T) {
		m := base()
		var diags diag.Diagnostics
		newResource(nil, errors.New("zone list failed")).resolveGatewayZoneID(context.Background(), &m, &diags)
		if !netDiagsContain(diags, "zone list failed") {
			t.Fatalf("want lookup diagnostic, got: %v", diags)
		}
	})

	t.Run("missing system Internal is diagnostic", func(t *testing.T) {
		m := base()
		var diags diag.Diagnostics
		newResource(nil, nil).resolveGatewayZoneID(context.Background(), &m, &diags)
		if !netDiagsContain(diags, "Unable to resolve gateway network zone") {
			t.Fatalf("want missing-zone diagnostic, got: %v", diags)
		}
	})
}

// netConfigOf builds a Config carrying the given model. tfsdk.Config has no
// Set, so we Set into a State (which does) and convert it (identical fields).
func netConfigOf(t *testing.T, m networkModel) tfsdk.Config {
	t.Helper()
	return tfsdk.Config(netStateOf(t, m))
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

// TestExpandNetworkGatewayMinimalDHCP proves that a DHCP block containing only
// its range still gets the required lease and conflict-detection wire values.
// The genuinely optional DNS and domain keys remain absent.
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
	if dhcp["leaseTimeSeconds"] != float64(defaultDHCPLeaseSecond) {
		t.Errorf("leaseTimeSeconds = %v, want %d", dhcp["leaseTimeSeconds"], defaultDHCPLeaseSecond)
	}
	if dhcp["pingConflictDetectionEnabled"] != defaultConflictCheck {
		t.Errorf("pingConflictDetectionEnabled = %v, want %v", dhcp["pingConflictDetectionEnabled"], defaultConflictCheck)
	}
	for _, absent := range []string{"dnsServerIpAddressesOverride", "domainName"} {
		if _, ok := dhcp[absent]; ok {
			t.Errorf("optional key %q should be absent when null, got %v", absent, dhcp[absent])
		}
	}
}

func TestFlattenNetworkEmptyOptionalDHCPValues(t *testing.T) {
	empty := ""
	lease := int32(3600)
	conflictCheck := true
	var dhcp official.GatewayManagedIPv4DHCPConfiguration
	if err := dhcp.FromGatewayManagedIPv4DHCPServerConfiguration(official.GatewayManagedIPv4DHCPServerConfiguration{
		Mode:                         dhcpModeServer,
		IpAddressRange:               &official.IPAddressRange{Start: "192.0.2.10", Stop: "192.0.2.20"},
		LeaseTimeSeconds:             &lease,
		DomainName:                   &empty,
		PingConflictDetectionEnabled: &conflictCheck,
	}); err != nil {
		t.Fatalf("build DHCP details: %v", err)
	}
	details := &official.NetworkDetails{
		Id: uuid.New(), Name: "empty-domain", VlanId: 20, Enabled: true, Management: mgmtGateway,
	}
	if err := details.FromGatewayManagedNetworkDetails(official.GatewayManagedNetworkDetails{
		Id: details.Id, Name: details.Name, VlanId: details.VlanId, Enabled: true, Management: mgmtGateway,
		Ipv4Configuration: &official.GatewayManagedIPv4Configuration{
			HostIpAddress: "192.0.2.1", PrefixLength: 24, DhcpConfiguration: &dhcp,
		},
	}); err != nil {
		t.Fatalf("build gateway details: %v", err)
	}
	model := flattenNetworkForTest(t, details)
	if model.Gateway == nil || model.Gateway.DHCP == nil {
		t.Fatal("gateway DHCP flattened to nil")
	}
	if !model.Gateway.DHCP.DomainName.IsNull() {
		t.Errorf("empty controller domain_name = %q, want Terraform null", model.Gateway.DHCP.DomainName.ValueString())
	}
}
