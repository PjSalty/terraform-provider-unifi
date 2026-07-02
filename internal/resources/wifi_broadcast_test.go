package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandWifiBroadcastWPA2 verifies the full nested-union chain marshals
// correctly: the STANDARD discriminator, the named fields surviving the union,
// the security variant (type + passphrase), the band list riding the union, and
// the SPECIFIC network binding.
func TestExpandWifiBroadcastWPA2(t *testing.T) {
	netID := uuid.New()
	freqs := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("2.4"), types.StringValue("5"),
	})
	model := wifiBroadcastModel{
		Name:            types.StringValue("IoT Devices"),
		Enabled:         types.BoolValue(true),
		HideName:        types.BoolValue(false),
		Security:        types.StringValue("WPA2_PERSONAL"),
		Passphrase:      types.StringValue("supersecret"),
		PmfMode:         types.StringValue("OPTIONAL"),
		Frequencies:     freqs,
		NetworkID:       types.StringValue(netID.String()),
		DeviceFilter:    types.SetNull(types.StringType),
		ClientIsolation: types.BoolValue(true),
	}

	body, diags := expandWifiBroadcast(context.Background(), model)
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

	if got["name"] != "IoT Devices" {
		t.Errorf("name = %v, want IoT Devices", got["name"])
	}
	if got["type"] != "STANDARD" {
		t.Errorf("type = %v, want STANDARD", got["type"])
	}
	if got["clientIsolationEnabled"] != true {
		t.Errorf("clientIsolationEnabled = %v, want true", got["clientIsolationEnabled"])
	}

	sec, ok := got["securityConfiguration"].(map[string]any)
	if !ok {
		t.Fatalf("securityConfiguration not an object: %v", got["securityConfiguration"])
	}
	if sec["type"] != "WPA2_PERSONAL" {
		t.Errorf("security type = %v, want WPA2_PERSONAL", sec["type"])
	}
	if sec["passphrase"] != "supersecret" {
		t.Errorf("passphrase = %v, want supersecret", sec["passphrase"])
	}
	if sec["pmfMode"] != "OPTIONAL" {
		t.Errorf("pmfMode = %v, want OPTIONAL", sec["pmfMode"])
	}

	bands, ok := got["broadcastingFrequenciesGHz"].([]any)
	if !ok || len(bands) != 2 {
		t.Errorf("broadcastingFrequenciesGHz = %v, want 2 bands", got["broadcastingFrequenciesGHz"])
	}

	net, ok := got["network"].(map[string]any)
	if !ok || net["type"] != "SPECIFIC" || net["networkId"] != netID.String() {
		t.Errorf("network = %v, want SPECIFIC binding to %s", got["network"], netID.String())
	}
}

func TestExpandWifiBroadcastOpen(t *testing.T) {
	model := wifiBroadcastModel{
		Name:            types.StringValue("Guest"),
		Enabled:         types.BoolValue(true),
		HideName:        types.BoolValue(false),
		Security:        types.StringValue("OPEN"),
		Passphrase:      types.StringNull(),
		PmfMode:         types.StringNull(),
		Frequencies:     types.SetNull(types.StringType),
		NetworkID:       types.StringNull(),
		DeviceFilter:    types.SetNull(types.StringType),
		ClientIsolation: types.BoolValue(false),
	}
	body, diags := expandWifiBroadcast(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	b, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	sec, _ := got["securityConfiguration"].(map[string]any)
	if sec["type"] != "OPEN" {
		t.Errorf("security type = %v, want OPEN", sec["type"])
	}
}
