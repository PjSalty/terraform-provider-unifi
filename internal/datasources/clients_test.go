package datasources

import (
	"encoding/json"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
)

// unmarshalOverview builds a ClientOverview the same way the go-unifi client
// does: from the wire JSON, so both the base fields and the private union blob
// are populated. Constructing the struct literally would leave the union nil and
// the As* accessors would see nothing, which is exactly the gotcha under test.
func unmarshalOverview(t *testing.T, raw string) official.ClientOverview {
	t.Helper()
	var c official.ClientOverview
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal ClientOverview: %v", err)
	}
	return c
}

// TestFlattenClientWired proves the WIRED branch pulls macAddress and
// uplinkDeviceId out of the union (they are not base fields).
func TestFlattenClientWired(t *testing.T) {
	c := unmarshalOverview(t, `{
		"type": "WIRED",
		"id": "11111111-1111-1111-1111-111111111111",
		"name": "nas",
		"ipAddress": "192.0.2.10",
		"connectedAt": "2026-06-24T12:00:00Z",
		"access": {"type": "DEFAULT"},
		"macAddress": "aa:bb:cc:dd:ee:ff",
		"uplinkDeviceId": "22222222-2222-2222-2222-222222222222"
	}`)

	m, err := flattenClient(c)
	if err != nil {
		t.Fatalf("flattenClient: %v", err)
	}
	if got := m.Type.ValueString(); got != "WIRED" {
		t.Errorf("type = %q, want WIRED", got)
	}
	if got := m.MacAddress.ValueString(); got != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("mac_address = %q, want aa:bb:cc:dd:ee:ff", got)
	}
	if got := m.UplinkDeviceID.ValueString(); got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("uplink_device_id = %q, want 22222222-...", got)
	}
	if got := m.IPAddress.ValueString(); got != "192.0.2.10" {
		t.Errorf("ip_address = %q, want 192.0.2.10", got)
	}
	if got := m.ConnectedAt.ValueString(); got != "2026-06-24T12:00:00Z" {
		t.Errorf("connected_at = %q, want 2026-06-24T12:00:00Z", got)
	}
}

// TestFlattenClientWireless proves the WIRELESS branch also reads MAC/uplink.
func TestFlattenClientWireless(t *testing.T) {
	c := unmarshalOverview(t, `{
		"type": "WIRELESS",
		"id": "33333333-3333-3333-3333-333333333333",
		"name": "phone",
		"ipAddress": "198.51.100.20",
		"access": {"type": "GUEST"},
		"macAddress": "11:22:33:44:55:66",
		"uplinkDeviceId": "44444444-4444-4444-4444-444444444444"
	}`)

	m, err := flattenClient(c)
	if err != nil {
		t.Fatalf("flattenClient: %v", err)
	}
	if got := m.MacAddress.ValueString(); got != "11:22:33:44:55:66" {
		t.Errorf("mac_address = %q, want 11:22:33:44:55:66", got)
	}
	if got := m.UplinkDeviceID.ValueString(); got != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("uplink_device_id = %q, want 44444444-...", got)
	}
}

// TestFlattenClientVPNNoMac proves VPN clients (no MAC on the variant) flatten to
// empty mac/uplink rather than erroring or leaking a stale value.
func TestFlattenClientVPNNoMac(t *testing.T) {
	c := unmarshalOverview(t, `{
		"type": "VPN",
		"id": "55555555-5555-5555-5555-555555555555",
		"name": "road-warrior",
		"access": {"type": "DEFAULT"}
	}`)

	m, err := flattenClient(c)
	if err != nil {
		t.Fatalf("flattenClient: %v", err)
	}
	if got := m.MacAddress.ValueString(); got != "" {
		t.Errorf("mac_address = %q, want empty for VPN", got)
	}
	if got := m.UplinkDeviceID.ValueString(); got != "" {
		t.Errorf("uplink_device_id = %q, want empty for VPN", got)
	}
	if got := m.IPAddress.ValueString(); got != "" {
		t.Errorf("ip_address = %q, want empty when not reported", got)
	}
	if got := m.ConnectedAt.ValueString(); got != "" {
		t.Errorf("connected_at = %q, want empty when not reported", got)
	}
}
