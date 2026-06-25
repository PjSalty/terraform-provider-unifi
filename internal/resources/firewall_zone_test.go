package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandFirewallZoneWithNetworks verifies the body marshals the name and the
// network IDs as a JSON array of UUID strings.
func TestExpandFirewallZoneWithNetworks(t *testing.T) {
	n1, n2 := uuid.New(), uuid.New()
	model := firewallZoneModel{
		Name: types.StringValue("IoT"),
		NetworkIDs: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue(n1.String()), types.StringValue(n2.String()),
		}),
	}

	body, diags := expandFirewallZone(context.Background(), model)
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

	if got["name"] != "IoT" {
		t.Errorf("name = %v, want IoT", got["name"])
	}
	ids, ok := got["networkIds"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("networkIds = %v, want 2 entries", got["networkIds"])
	}
	want := map[string]bool{n1.String(): true, n2.String(): true}
	for _, v := range ids {
		s, _ := v.(string)
		if !want[s] {
			t.Errorf("unexpected networkId %v", v)
		}
	}
}

// TestExpandFirewallZoneNoNetworks verifies a null set marshals to an empty array
// (not null), since the API field is a non-omitempty slice.
func TestExpandFirewallZoneNoNetworks(t *testing.T) {
	model := firewallZoneModel{
		Name:       types.StringValue("Quarantine"),
		NetworkIDs: types.SetNull(types.StringType),
	}
	body, diags := expandFirewallZone(context.Background(), model)
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
	ids, ok := got["networkIds"].([]any)
	if !ok {
		t.Fatalf("networkIds = %v, want empty array", got["networkIds"])
	}
	if len(ids) != 0 {
		t.Errorf("networkIds = %v, want empty array", ids)
	}
}
