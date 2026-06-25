package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandACLRuleIPV4 proves the union/named-field ordering: the IPV4
// discriminator from the variant, the named scalar fields surviving the union,
// the typed source/destination subnet filters surviving as objects (not the
// nil interface{} the marshaler would otherwise emit as null), and the
// protocolFilter riding the variant.
func TestExpandACLRuleIPV4(t *testing.T) {
	model := aclRuleModel{
		Type:        types.StringValue("IPV4"),
		Name:        types.StringValue("block-guest-to-lan"),
		Action:      types.StringValue("BLOCK"),
		Enabled:     types.BoolValue(true),
		Description: types.StringValue("guest isolation"),
		ProtocolFilter: types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("TCP"), types.StringValue("UDP"),
		}),
		NetworkIDFilter: types.StringNull(),
		SourceFilter: &aclRuleEndpointModel{
			IPAddressesOrSubnets: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("10.10.50.0/24"),
			}),
			Ports:        types.ListNull(types.Int64Type),
			MACAddresses: types.ListNull(types.StringType),
			PrefixLength: types.Int64Null(),
		},
		DestinationFilter: &aclRuleEndpointModel{
			IPAddressesOrSubnets: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("10.10.20.0/24"),
			}),
			Ports: types.ListValueMust(types.Int64Type, []attr.Value{
				types.Int64Value(443),
			}),
			MACAddresses: types.ListNull(types.StringType),
			PrefixLength: types.Int64Null(),
		},
	}

	body, diags := expandACLRule(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := mustMarshalToMap(t, body)

	if got["type"] != "IPV4" {
		t.Errorf("type = %v, want IPV4", got["type"])
	}
	if got["name"] != "block-guest-to-lan" {
		t.Errorf("name = %v, want block-guest-to-lan", got["name"])
	}
	if got["action"] != "BLOCK" {
		t.Errorf("action = %v, want BLOCK", got["action"])
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["description"] != "guest isolation" {
		t.Errorf("description = %v, want guest isolation", got["description"])
	}

	protos, ok := got["protocolFilter"].([]any)
	if !ok || len(protos) != 2 {
		t.Errorf("protocolFilter = %v, want 2 protocols", got["protocolFilter"])
	}

	src, ok := got["sourceFilter"].(map[string]any)
	if !ok {
		t.Fatalf("sourceFilter not an object (union override failed): %v", got["sourceFilter"])
	}
	if src["type"] != "IP_ADDRESSES_OR_SUBNETS" {
		t.Errorf("sourceFilter.type = %v, want IP_ADDRESSES_OR_SUBNETS", src["type"])
	}
	subnets, ok := src["ipAddressesOrSubnets"].([]any)
	if !ok || len(subnets) != 1 || subnets[0] != "10.10.50.0/24" {
		t.Errorf("sourceFilter.ipAddressesOrSubnets = %v, want [10.10.50.0/24]", src["ipAddressesOrSubnets"])
	}

	dst, ok := got["destinationFilter"].(map[string]any)
	if !ok {
		t.Fatalf("destinationFilter not an object: %v", got["destinationFilter"])
	}
	ports, ok := dst["portFilter"].([]any)
	if !ok || len(ports) != 1 || ports[0] != float64(443) {
		t.Errorf("destinationFilter.portFilter = %v, want [443]", dst["portFilter"])
	}
}

// TestExpandACLRuleMAC proves the MAC discriminator, the typed MAC endpoint
// filter surviving the union override, and networkIdFilter riding the variant.
func TestExpandACLRuleMAC(t *testing.T) {
	netID := uuid.New()
	model := aclRuleModel{
		Type:            types.StringValue("MAC"),
		Name:            types.StringValue("block-mac"),
		Action:          types.StringValue("ALLOW"),
		Enabled:         types.BoolValue(true),
		Description:     types.StringNull(),
		ProtocolFilter:  types.ListNull(types.StringType),
		NetworkIDFilter: types.StringValue(netID.String()),
		SourceFilter: &aclRuleEndpointModel{
			IPAddressesOrSubnets: types.ListNull(types.StringType),
			Ports:                types.ListNull(types.Int64Type),
			MACAddresses: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("aa:bb:cc:dd:ee:ff"),
			}),
			PrefixLength: types.Int64Value(48),
		},
		DestinationFilter: nil,
	}

	body, diags := expandACLRule(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := mustMarshalToMap(t, body)

	if got["type"] != "MAC" {
		t.Errorf("type = %v, want MAC", got["type"])
	}
	if got["action"] != "ALLOW" {
		t.Errorf("action = %v, want ALLOW", got["action"])
	}
	if got["networkIdFilter"] != netID.String() {
		t.Errorf("networkIdFilter = %v, want %s", got["networkIdFilter"], netID.String())
	}
	if _, present := got["description"]; present {
		t.Errorf("description should be omitted when null, got %v", got["description"])
	}

	src, ok := got["sourceFilter"].(map[string]any)
	if !ok {
		t.Fatalf("sourceFilter not an object: %v", got["sourceFilter"])
	}
	if src["type"] != "MAC_ADDRESSES" {
		t.Errorf("sourceFilter.type = %v, want MAC_ADDRESSES", src["type"])
	}
	macs, ok := src["macAddresses"].([]any)
	if !ok || len(macs) != 1 || macs[0] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("sourceFilter.macAddresses = %v, want [aa:bb:cc:dd:ee:ff]", src["macAddresses"])
	}
	if src["prefixLength"] != float64(48) {
		t.Errorf("sourceFilter.prefixLength = %v, want 48", src["prefixLength"])
	}
}

func mustMarshalToMap(t *testing.T, body any) map[string]any {
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
