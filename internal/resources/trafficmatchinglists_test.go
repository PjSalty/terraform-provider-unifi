package resources

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandTrafficMatchingListIPv4 proves the nested-union chain marshals: the
// outer name/type survive the union override, and items ride the union with the
// right per-item discriminators and value/start/stop fields.
func TestExpandTrafficMatchingListIPv4(t *testing.T) {
	model := trafficMatchingListModel{
		Name: types.StringValue("blocklist"),
		Type: types.StringValue("IPV4_ADDRESSES"),
		Items: []trafficMatchingListItemModel{
			{MatchType: types.StringValue("IP_ADDRESS"), Value: types.StringValue("10.0.0.5")},
			{MatchType: types.StringValue("SUBNET"), Value: types.StringValue("192.168.0.0/24")},
			{MatchType: types.StringValue("IP_ADDRESS_RANGE"), Start: types.StringValue("10.0.0.1"), Stop: types.StringValue("10.0.0.9")},
		},
	}
	got := marshalTML(t, model)

	if got["name"] != "blocklist" {
		t.Errorf("name = %v, want blocklist", got["name"])
	}
	if got["type"] != "IPV4_ADDRESSES" {
		t.Errorf("type = %v, want IPV4_ADDRESSES", got["type"])
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("items = %v, want 3 entries", got["items"])
	}

	addr := items[0].(map[string]any)
	if addr["type"] != "IP_ADDRESS" || addr["value"] != "10.0.0.5" {
		t.Errorf("item[0] = %v, want IP_ADDRESS 10.0.0.5", addr)
	}
	subnet := items[1].(map[string]any)
	if subnet["type"] != "SUBNET" || subnet["value"] != "192.168.0.0/24" {
		t.Errorf("item[1] = %v, want SUBNET 192.168.0.0/24", subnet)
	}
	rng := items[2].(map[string]any)
	if rng["type"] != "IP_ADDRESS_RANGE" || rng["start"] != "10.0.0.1" || rng["stop"] != "10.0.0.9" {
		t.Errorf("item[2] = %v, want IP_ADDRESS_RANGE 10.0.0.1-10.0.0.9", rng)
	}
}

func TestExpandTrafficMatchingListPorts(t *testing.T) {
	model := trafficMatchingListModel{
		Name: types.StringValue("svc-ports"),
		Type: types.StringValue("PORTS"),
		Items: []trafficMatchingListItemModel{
			{MatchType: types.StringValue("PORT_NUMBER"), Value: types.StringValue("443")},
			{MatchType: types.StringValue("PORT_NUMBER_RANGE"), Start: types.StringValue("8000"), Stop: types.StringValue("8100")},
		},
	}
	got := marshalTML(t, model)

	if got["type"] != "PORTS" {
		t.Errorf("type = %v, want PORTS", got["type"])
	}
	items := got["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	// Port numbers must be emitted as JSON numbers (the variant field is int32),
	// not strings, even though the Terraform value comes in as a string.
	num := items[0].(map[string]any)
	if num["type"] != "PORT_NUMBER" || num["value"].(float64) != 443 {
		t.Errorf("item[0] = %v, want PORT_NUMBER 443 (number)", num)
	}
	rng := items[1].(map[string]any)
	if rng["type"] != "PORT_NUMBER_RANGE" || rng["start"].(float64) != 8000 || rng["stop"].(float64) != 8100 {
		t.Errorf("item[1] = %v, want PORT_NUMBER_RANGE 8000-8100 (numbers)", rng)
	}
}

func TestExpandTrafficMatchingListPortRejectsNonInt(t *testing.T) {
	model := trafficMatchingListModel{
		Name: types.StringValue("bad"),
		Type: types.StringValue("PORTS"),
		Items: []trafficMatchingListItemModel{
			{MatchType: types.StringValue("PORT_NUMBER"), Value: types.StringValue("https")},
		},
	}
	_, diags := expandTrafficMatchingList(model)
	if !diags.HasError() {
		t.Fatalf("expected an error for non-integer port, got none")
	}
}

func marshalTML(t *testing.T, model trafficMatchingListModel) map[string]any {
	t.Helper()
	body, diags := expandTrafficMatchingList(model)
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
