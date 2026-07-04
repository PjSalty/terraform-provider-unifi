package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// expandNetworkForTest runs expandNetwork and fails on any diagnostic.
func expandNetworkForTest(t *testing.T, m networkModel) official.NetworkCreateOrUpdate {
	t.Helper()
	body, diags := expandNetwork(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("expandNetwork diags: %v", diags)
	}
	return body
}

// marshalToMap marshals the body and returns it as a generic map, failing on error.
func marshalToMap(t *testing.T, body official.NetworkCreateOrUpdate) map[string]any {
	t.Helper()
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

// TestExpandNetworkMarshalsUnmanaged guards the union gotcha: the named fields
// must survive into the marshaled body (management UNMANAGED, name/vlan/enabled
// present), not be lost to an empty union.
func TestExpandNetworkMarshalsUnmanaged(t *testing.T) {
	got := marshalToMap(t, expandNetworkForTest(t, networkModel{
		Name:    types.StringValue("IoT Devices"),
		VlanID:  types.Int64Value(81),
		Enabled: types.BoolValue(true),
	}))
	if got["name"] != "IoT Devices" {
		t.Errorf("name = %v, want IoT Devices", got["name"])
	}
	if got["management"] != "UNMANAGED" {
		t.Errorf("management = %v, want UNMANAGED", got["management"])
	}
	if got["vlanId"] != float64(81) {
		t.Errorf("vlanId = %v, want 81", got["vlanId"])
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if _, ok := got["ipv4Configuration"]; ok {
		t.Error("unmanaged network must not carry ipv4Configuration")
	}
}

// TestExpandNetworkMarshalsGateway asserts a gateway-managed network marshals
// management GATEWAY plus the L3 subnet, and that the named fields still win
// over the union.
func TestExpandNetworkMarshalsGateway(t *testing.T) {
	got := marshalToMap(t, expandNetworkForTest(t, networkModel{
		Name:       types.StringValue("Corp"),
		VlanID:     types.Int64Value(10),
		Enabled:    types.BoolValue(true),
		Management: types.StringValue("GATEWAY"),
		Gateway: &gatewayModel{
			HostIPAddress:    types.StringValue("192.168.10.1"),
			PrefixLength:     types.Int64Value(24),
			AutoScaleEnabled: types.BoolValue(false),
			DHCP:             nil,
		},
	}))
	if got["management"] != "GATEWAY" {
		t.Fatalf("management = %v, want GATEWAY", got["management"])
	}
	if got["name"] != "Corp" || got["vlanId"] != float64(10) || got["enabled"] != true {
		t.Errorf("named fields lost: name=%v vlan=%v enabled=%v", got["name"], got["vlanId"], got["enabled"])
	}
	ipv4, ok := got["ipv4Configuration"].(map[string]any)
	if !ok {
		t.Fatalf("ipv4Configuration missing/not object: %v", got["ipv4Configuration"])
	}
	if ipv4["hostIpAddress"] != "192.168.10.1" {
		t.Errorf("hostIpAddress = %v, want 192.168.10.1", ipv4["hostIpAddress"])
	}
	if ipv4["prefixLength"] != float64(24) {
		t.Errorf("prefixLength = %v, want 24", ipv4["prefixLength"])
	}
	if _, ok := ipv4["dhcpConfiguration"]; ok {
		t.Error("no dhcp block => dhcpConfiguration must be absent")
	}
}

// TestExpandNetworkMarshalsGatewayDHCP asserts the nested DHCP server union
// (mode SERVER + range + dns + domain + lease) marshals correctly.
func TestExpandNetworkMarshalsGatewayDHCP(t *testing.T) {
	dns, d := types.ListValueFrom(context.Background(), types.StringType, []string{"10.10.20.13", "1.1.1.1"})
	if d.HasError() {
		t.Fatalf("build dns list: %v", d)
	}
	got := marshalToMap(t, expandNetworkForTest(t, networkModel{
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
				DNSServers:       dns,
				DomainName:       types.StringValue("corp.lan"),
				LeaseTimeSeconds: types.Int64Value(86400),
			},
		},
	}))
	ipv4 := got["ipv4Configuration"].(map[string]any)
	dhcp, ok := ipv4["dhcpConfiguration"].(map[string]any)
	if !ok {
		t.Fatalf("dhcpConfiguration missing: %v", ipv4["dhcpConfiguration"])
	}
	if dhcp["mode"] != "SERVER" {
		t.Errorf("dhcp mode = %v, want SERVER", dhcp["mode"])
	}
	rng, ok := dhcp["ipAddressRange"].(map[string]any)
	if !ok || rng["start"] != "192.168.10.100" || rng["stop"] != "192.168.10.200" {
		t.Errorf("ipAddressRange = %v", dhcp["ipAddressRange"])
	}
	if dhcp["domainName"] != "corp.lan" {
		t.Errorf("domainName = %v, want corp.lan", dhcp["domainName"])
	}
	if dhcp["leaseTimeSeconds"] != float64(86400) {
		t.Errorf("leaseTimeSeconds = %v, want 86400", dhcp["leaseTimeSeconds"])
	}
	dnsOut, ok := dhcp["dnsServerIpAddressesOverride"].([]any)
	if !ok || len(dnsOut) != 2 || dnsOut[0] != "10.10.20.13" {
		t.Errorf("dnsServerIpAddressesOverride = %v", dhcp["dnsServerIpAddressesOverride"])
	}
}

func flattenNetworkForTest(t *testing.T, n *official.NetworkDetails) networkModel {
	t.Helper()
	m, diags := flattenNetwork(context.Background(), n)
	if diags.HasError() {
		t.Fatalf("flattenNetwork diags: %v", diags)
	}
	return m
}

func TestFlattenNetwork(t *testing.T) {
	id := uuid.New()
	m := flattenNetworkForTest(t, &official.NetworkDetails{
		Id:         id,
		Name:       "Trusted",
		VlanId:     70,
		Enabled:    true,
		Management: "UNMANAGED",
	})
	if m.ID.ValueString() != id.String() {
		t.Errorf("id = %q, want %q", m.ID.ValueString(), id.String())
	}
	if m.Name.ValueString() != "Trusted" {
		t.Errorf("name = %q, want Trusted", m.Name.ValueString())
	}
	if m.VlanID.ValueInt64() != 70 {
		t.Errorf("vlan_id = %d, want 70", m.VlanID.ValueInt64())
	}
	if !m.Enabled.ValueBool() {
		t.Error("enabled = false, want true")
	}
	if m.Management.ValueString() != "UNMANAGED" {
		t.Errorf("management = %q, want UNMANAGED", m.Management.ValueString())
	}
	if m.Gateway != nil {
		t.Error("unmanaged network must flatten to nil gateway")
	}
}

// TestNetworkRoundTripGatewayDHCP builds a gateway body, marshals it, unmarshals
// as NetworkDetails, and flattens it back, asserting the L3 + DHCP survive the
// full round trip through the controller wire format.
func TestNetworkRoundTripGatewayDHCP(t *testing.T) {
	dns, _ := types.ListValueFrom(context.Background(), types.StringType, []string{"10.10.20.13"})
	body := expandNetworkForTest(t, networkModel{
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
				DNSServers:       dns,
				DomainName:       types.StringValue("corp.lan"),
				LeaseTimeSeconds: types.Int64Value(86400),
			},
		},
	})
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	// The wire body carries no id; inject one so flatten has a stable UUID.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	id := uuid.New()
	obj["id"], _ = json.Marshal(id.String())
	obj["default"], _ = json.Marshal(false)
	b2, _ := json.Marshal(obj)

	var details official.NetworkDetails
	if err := json.Unmarshal(b2, &details); err != nil {
		t.Fatalf("unmarshal NetworkDetails: %v", err)
	}
	m := flattenNetworkForTest(t, &details)
	if m.Management.ValueString() != "GATEWAY" {
		t.Fatalf("management = %q, want GATEWAY", m.Management.ValueString())
	}
	if m.Gateway == nil {
		t.Fatal("gateway flattened to nil")
	}
	if m.Gateway.HostIPAddress.ValueString() != "192.168.10.1" {
		t.Errorf("host_ip_address = %q", m.Gateway.HostIPAddress.ValueString())
	}
	if m.Gateway.PrefixLength.ValueInt64() != 24 {
		t.Errorf("prefix_length = %d, want 24", m.Gateway.PrefixLength.ValueInt64())
	}
	if m.Gateway.DHCP == nil {
		t.Fatal("dhcp flattened to nil")
	}
	if m.Gateway.DHCP.RangeStart.ValueString() != "192.168.10.100" {
		t.Errorf("range_start = %q", m.Gateway.DHCP.RangeStart.ValueString())
	}
	if m.Gateway.DHCP.DomainName.ValueString() != "corp.lan" {
		t.Errorf("domain_name = %q", m.Gateway.DHCP.DomainName.ValueString())
	}
	if m.Gateway.DHCP.LeaseTimeSeconds.ValueInt64() != 86400 {
		t.Errorf("lease_time_seconds = %d, want 86400", m.Gateway.DHCP.LeaseTimeSeconds.ValueInt64())
	}
	var dnsOut []string
	if d := m.Gateway.DHCP.DNSServers.ElementsAs(context.Background(), &dnsOut, false); d.HasError() {
		t.Fatalf("dns elements: %v", d)
	}
	if len(dnsOut) != 1 || dnsOut[0] != "10.10.20.13" {
		t.Errorf("dns_servers = %v, want [10.10.20.13]", dnsOut)
	}
}
