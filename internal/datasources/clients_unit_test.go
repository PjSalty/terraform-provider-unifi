package datasources

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

func TestClientsMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewClientsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_clients" {
		t.Errorf("type name = %q, want unifi_clients", resp.TypeName)
	}
}

func TestClientsSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewClientsDataSource().(*clientsDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["clients"]; !ok {
		t.Error("schema missing clients attribute")
	}
}

func TestClientsConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewClientsDataSource().(*clientsDataSource)
		var resp datasource.ConfigureResponse
		ds.Configure(context.Background(), datasource.ConfigureRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if ds.data != nil {
			t.Error("data set from nil provider data")
		}
	})
	t.Run("wrong type errors", func(t *testing.T) {
		ds := NewClientsDataSource().(*clientsDataSource)
		var resp datasource.ConfigureResponse
		ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for wrong provider data type")
		}
		if ds.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		ds := NewClientsDataSource().(*clientsDataSource)
		pd := testutil.Data(&official.ClientMock{})
		var resp datasource.ConfigureResponse
		ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: pd}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if ds.data != pd {
			t.Error("data not stored")
		}
	})
}

// clientsClientWith returns an official client whose Clients().ListConnectedAll
// yields the given overviews, recording the site and filter it was called with.
func clientsClientWith(items []official.ClientOverview, trailing error, gotSite *uuid.UUID, gotFilter *string) *official.ClientMock {
	return &official.ClientMock{
		ClientsFunc: func() official.ClientsClient {
			return &official.ClientsClientMock{
				ListConnectedAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.ClientOverview, error] {
					if gotSite != nil {
						*gotSite = siteID
					}
					if gotFilter != nil {
						*gotFilter = filter
					}
					return seqOf(items, trailing)
				},
			}
		},
	}
}

func TestClientsRead(t *testing.T) {
	wired := unmarshalOverview(t, `{
		"type": "WIRED",
		"id": "11111111-1111-4111-8111-111111111111",
		"name": "nas",
		"ipAddress": "192.0.2.10",
		"connectedAt": "2026-06-24T12:00:00Z",
		"access": {"type": "DEFAULT"},
		"macAddress": "aa:bb:cc:dd:ee:ff",
		"uplinkDeviceId": "22222222-2222-4222-8222-222222222222"
	}`)
	// No uplinkDeviceId: exercises the nil branch of derefUUID through Read.
	wiredNoUplink := unmarshalOverview(t, `{
		"type": "WIRED",
		"id": "33333333-3333-4333-8333-333333333333",
		"name": "printer",
		"ipAddress": "198.51.100.40",
		"access": {"type": "DEFAULT"},
		"macAddress": "11:22:33:44:55:66"
	}`)
	vpn := unmarshalOverview(t, `{
		"type": "VPN",
		"id": "44444444-4444-4444-8444-444444444444",
		"name": "road-warrior",
		"access": {"type": "DEFAULT"}
	}`)

	var gotSite uuid.UUID
	var gotFilter string
	oc := clientsClientWith([]official.ClientOverview{wired, wiredNoUplink, vpn}, nil, &gotSite, &gotFilter)
	ds := NewClientsDataSource().(*clientsDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if gotSite != testutil.SiteID {
		t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
	}
	if gotFilter != "" {
		t.Errorf("filter = %q, want empty", gotFilter)
	}

	var got clientsModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Clients) != 3 {
		t.Fatalf("clients = %d, want 3", len(got.Clients))
	}
	first := got.Clients[0]
	if first.Name.ValueString() != "nas" {
		t.Errorf("name = %q, want nas", first.Name.ValueString())
	}
	if first.MacAddress.ValueString() != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("mac_address = %q, want aa:bb:cc:dd:ee:ff", first.MacAddress.ValueString())
	}
	if first.UplinkDeviceID.ValueString() != "22222222-2222-4222-8222-222222222222" {
		t.Errorf("uplink_device_id = %q, want 22222222-...", first.UplinkDeviceID.ValueString())
	}
	if first.ConnectedAt.ValueString() != "2026-06-24T12:00:00Z" {
		t.Errorf("connected_at = %q, want 2026-06-24T12:00:00Z", first.ConnectedAt.ValueString())
	}
	if got.Clients[1].UplinkDeviceID.ValueString() != "" {
		t.Errorf("uplink_device_id = %q, want empty when not reported", got.Clients[1].UplinkDeviceID.ValueString())
	}
	if got.Clients[2].Type.ValueString() != "VPN" {
		t.Errorf("type = %q, want VPN", got.Clients[2].Type.ValueString())
	}
}

// TestClientsReadListError proves an API failure mid-drain surfaces as a
// diagnostic instead of a partial state.
func TestClientsReadListError(t *testing.T) {
	oc := clientsClientWith(nil, errors.New("controller unreachable"), nil, nil)
	ds := NewClientsDataSource().(*clientsDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to list clients") {
		t.Fatalf("expected list-clients diagnostic, got: %v", resp.Diagnostics)
	}
	if !hasErrorContaining(&resp, "controller unreachable") {
		t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
	}
}

// TestClientsReadDecodeError proves a WIRED overview whose union blob is absent
// (a literally constructed struct never carries one) fails flattening and Read
// reports it rather than silently dropping the client.
func TestClientsReadDecodeError(t *testing.T) {
	broken := official.ClientOverview{
		Id:   uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		Name: "mystery",
		Type: "WIRED",
	}
	oc := clientsClientWith([]official.ClientOverview{broken}, nil, nil, nil)
	ds := NewClientsDataSource().(*clientsDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to decode client") {
		t.Fatalf("expected decode diagnostic, got: %v", resp.Diagnostics)
	}
}

// TestFlattenClientWiredDecodeError covers the WIRED error return directly.
func TestFlattenClientWiredDecodeError(t *testing.T) {
	_, err := flattenClient(official.ClientOverview{Type: "WIRED"})
	if err == nil {
		t.Fatal("expected error for WIRED overview without union data")
	}
}

// TestFlattenClientWirelessDecodeError covers the WIRELESS error return directly.
func TestFlattenClientWirelessDecodeError(t *testing.T) {
	_, err := flattenClient(official.ClientOverview{Type: "WIRELESS"})
	if err == nil {
		t.Fatal("expected error for WIRELESS overview without union data")
	}
}
