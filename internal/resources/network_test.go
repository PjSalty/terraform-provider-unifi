package resources

import (
	"encoding/json"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandNetworkMarshalsUnmanaged guards the union gotcha: the named fields
// must survive into the marshaled body (management UNMANAGED, name/vlan/enabled
// present), not be lost to an empty union.
func TestExpandNetworkMarshalsUnmanaged(t *testing.T) {
	body := expandNetwork(networkModel{
		Name:    types.StringValue("Remote-IoT"),
		VlanID:  types.Int64Value(81),
		Enabled: types.BoolValue(true),
	})
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "Remote-IoT" {
		t.Errorf("name = %v, want Remote-IoT", got["name"])
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
}

func TestFlattenNetwork(t *testing.T) {
	id := uuid.New()
	m := flattenNetwork(&official.NetworkDetails{
		Id:      id,
		Name:    "Trusted",
		VlanId:  70,
		Enabled: true,
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
}
