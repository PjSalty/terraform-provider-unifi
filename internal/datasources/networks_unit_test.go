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

func TestNetworksMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewNetworksDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_networks" {
		t.Errorf("type name = %q, want unifi_networks", resp.TypeName)
	}
}

func TestNetworksSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewNetworksDataSource().(*networksDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["networks"]; !ok {
		t.Error("schema missing networks attribute")
	}
}

func TestNetworksConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewNetworksDataSource().(*networksDataSource)
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
		ds := NewNetworksDataSource().(*networksDataSource)
		var resp datasource.ConfigureResponse
		ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-provider-data"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for wrong provider data type")
		}
		if ds.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		ds := NewNetworksDataSource().(*networksDataSource)
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

func TestNetworksRead(t *testing.T) {
	lanID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	iotID := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	tests := []struct {
		name      string
		list      iter.Seq2[official.NetworkOverview, error]
		wantErr   string
		wantCount int
	}{
		{
			name: "success maps every field",
			list: seqOf([]official.NetworkOverview{
				{Id: lanID, Name: "Default", VlanId: 1, Enabled: true, Default: true, Management: "GATEWAY"},
				{Id: iotID, Name: "IoT", VlanId: 81, Enabled: true, Default: false, Management: "SWITCH"},
			}, nil),
			wantCount: 2,
		},
		{
			name:    "list error surfaces as diagnostic",
			list:    seqOf[official.NetworkOverview](nil, errors.New("controller unreachable")),
			wantErr: "controller unreachable",
		},
		{
			name:      "empty list yields zero networks",
			list:      seqOf([]official.NetworkOverview{}, nil),
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotSite uuid.UUID
			var gotFilter string
			oc := &official.ClientMock{
				NetworksFunc: func() official.NetworksClient {
					return &official.NetworksClientMock{
						ListAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.NetworkOverview, error] {
							gotSite = siteID
							gotFilter = filter
							return tc.list
						},
					}
				},
			}
			ds := NewNetworksDataSource().(*networksDataSource)
			state := configureDS(t, ds, oc)

			resp := datasource.ReadResponse{State: state}
			ds.Read(context.Background(), datasource.ReadRequest{}, &resp)

			if tc.wantErr != "" {
				if !hasErrorContaining(&resp, "Failed to list networks") {
					t.Fatalf("expected list-networks diagnostic, got: %v", resp.Diagnostics)
				}
				if !hasErrorContaining(&resp, tc.wantErr) {
					t.Errorf("diagnostic missing underlying error %q: %v", tc.wantErr, resp.Diagnostics)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("read diagnostics: %v", resp.Diagnostics)
			}
			if gotSite != testutil.SiteID {
				t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
			}
			if gotFilter != "" {
				t.Errorf("filter = %q, want empty", gotFilter)
			}

			var got networksModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state get: %v", diags)
			}
			if len(got.Networks) != tc.wantCount {
				t.Fatalf("networks = %d, want %d", len(got.Networks), tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}

			first := got.Networks[0]
			if first.ID.ValueString() != lanID.String() {
				t.Errorf("id = %q, want %q", first.ID.ValueString(), lanID.String())
			}
			if first.Name.ValueString() != "Default" {
				t.Errorf("name = %q, want Default", first.Name.ValueString())
			}
			if first.VlanID.ValueInt64() != 1 {
				t.Errorf("vlan_id = %d, want 1", first.VlanID.ValueInt64())
			}
			if !first.Enabled.ValueBool() {
				t.Error("enabled = false, want true")
			}
			if !first.Default.ValueBool() {
				t.Error("default = false, want true")
			}
			if first.Management.ValueString() != "GATEWAY" {
				t.Errorf("management = %q, want GATEWAY", first.Management.ValueString())
			}

			second := got.Networks[1]
			if second.ID.ValueString() != iotID.String() {
				t.Errorf("second id = %q, want %q", second.ID.ValueString(), iotID.String())
			}
			if second.VlanID.ValueInt64() != 81 {
				t.Errorf("second vlan_id = %d, want 81", second.VlanID.ValueInt64())
			}
			if second.Default.ValueBool() {
				t.Error("second default = true, want false")
			}
			if second.Management.ValueString() != "SWITCH" {
				t.Errorf("second management = %q, want SWITCH", second.Management.ValueString())
			}
		})
	}
}
