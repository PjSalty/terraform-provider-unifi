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

func TestFirewallZonesMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewFirewallZonesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_firewall_zones" {
		t.Errorf("type name = %q, want unifi_firewall_zones", resp.TypeName)
	}
}

func TestFirewallZonesSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewFirewallZonesDataSource().(*firewallZonesDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["firewall_zones"]; !ok {
		t.Error("schema missing firewall_zones attribute")
	}
}

func TestFirewallZonesConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewFirewallZonesDataSource().(*firewallZonesDataSource)
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
		ds := NewFirewallZonesDataSource().(*firewallZonesDataSource)
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
		ds := NewFirewallZonesDataSource().(*firewallZonesDataSource)
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

// TestFirewallZonesRead exercises Read across the success, empty, and API-error
// paths from one table, asserting every field maps onto state for the success
// case (including the SYSTEM-vs-USER origin and the network_ids list).
func TestFirewallZonesRead(t *testing.T) {
	lanID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	extID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	netA := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	netB := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")

	tests := []struct {
		name      string
		zones     []official.FirewallZone
		listErr   error
		wantErr   string
		wantCount int
		verify    func(t *testing.T, got firewallZonesModel)
	}{
		{
			name: "success maps every field",
			zones: []official.FirewallZone{
				{
					Id:         lanID,
					Name:       "Internal",
					NetworkIds: []uuid.UUID{netA, netB},
					Metadata:   official.UserOrSystemDefinedEntityMetadata{Origin: "SYSTEM"},
				},
				{
					Id:         extID,
					Name:       "Custom",
					NetworkIds: nil,
					Metadata:   official.UserOrSystemDefinedEntityMetadata{Origin: "USER"},
				},
			},
			wantCount: 2,
			verify: func(t *testing.T, got firewallZonesModel) {
				first := got.FirewallZones[0]
				if first.ID.ValueString() != lanID.String() {
					t.Errorf("id = %q, want %q", first.ID.ValueString(), lanID.String())
				}
				if first.Name.ValueString() != "Internal" {
					t.Errorf("name = %q, want Internal", first.Name.ValueString())
				}
				if first.Origin.ValueString() != "SYSTEM" {
					t.Errorf("origin = %q, want SYSTEM", first.Origin.ValueString())
				}
				var ids []string
				if diags := first.NetworkIDs.ElementsAs(context.Background(), &ids, false); diags.HasError() {
					t.Fatalf("network_ids elements: %v", diags)
				}
				if len(ids) != 2 || ids[0] != netA.String() || ids[1] != netB.String() {
					t.Errorf("network_ids = %v, want [%s %s]", ids, netA, netB)
				}

				second := got.FirewallZones[1]
				if second.ID.ValueString() != extID.String() {
					t.Errorf("second id = %q, want %q", second.ID.ValueString(), extID.String())
				}
				if second.Origin.ValueString() != "USER" {
					t.Errorf("second origin = %q, want USER", second.Origin.ValueString())
				}
				var secondIDs []string
				if diags := second.NetworkIDs.ElementsAs(context.Background(), &secondIDs, false); diags.HasError() {
					t.Fatalf("second network_ids elements: %v", diags)
				}
				if len(secondIDs) != 0 {
					t.Errorf("second network_ids = %v, want empty", secondIDs)
				}
			},
		},
		{
			name:      "empty yields zero zones",
			zones:     []official.FirewallZone{},
			wantCount: 0,
		},
		{
			name:    "list error surfaces diagnostic",
			listErr: errors.New("controller unreachable"),
			wantErr: "controller unreachable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSite uuid.UUID
			var gotFilter string
			oc := &official.ClientMock{
				FirewallFunc: func() official.FirewallClient {
					return &official.FirewallClientMock{
						ListZonesAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.FirewallZone, error] {
							gotSite = siteID
							gotFilter = filter
							return seqOf(tt.zones, tt.listErr)
						},
					}
				},
			}
			ds := NewFirewallZonesDataSource().(*firewallZonesDataSource)
			state := configureDS(t, ds, oc)

			resp := datasource.ReadResponse{State: state}
			ds.Read(context.Background(), datasource.ReadRequest{}, &resp)

			if tt.wantErr != "" {
				if !hasErrorContaining(&resp, "Failed to list firewall zones") {
					t.Fatalf("expected list-zones diagnostic, got: %v", resp.Diagnostics)
				}
				if !hasErrorContaining(&resp, tt.wantErr) {
					t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
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

			var got firewallZonesModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state get: %v", diags)
			}
			if len(got.FirewallZones) != tt.wantCount {
				t.Fatalf("firewall_zones = %d, want %d", len(got.FirewallZones), tt.wantCount)
			}
			if tt.verify != nil {
				tt.verify(t, got)
			}
		})
	}
}
