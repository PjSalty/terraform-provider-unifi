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

func TestDeviceTagsMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewDeviceTagsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_device_tags" {
		t.Errorf("type name = %q, want unifi_device_tags", resp.TypeName)
	}
}

func TestDeviceTagsSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewDeviceTagsDataSource().(*deviceTagsDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["device_tags"]; !ok {
		t.Error("schema missing device_tags attribute")
	}
}

func TestDeviceTagsConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewDeviceTagsDataSource().(*deviceTagsDataSource)
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
		ds := NewDeviceTagsDataSource().(*deviceTagsDataSource)
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
		ds := NewDeviceTagsDataSource().(*deviceTagsDataSource)
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

func TestDeviceTagsRead(t *testing.T) {
	tag1 := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tag2 := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	dev1 := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	dev2 := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	dev3 := uuid.MustParse("33333333-3333-4333-8333-333333333333")

	tests := []struct {
		name    string
		seq     iter.Seq2[official.DeviceTag, error]
		wantErr string
		check   func(t *testing.T, got deviceTagsModel)
	}{
		{
			name: "two tags map onto state",
			seq: seqOf([]official.DeviceTag{
				{Id: tag1, Name: "cameras", DeviceIds: []uuid.UUID{dev1, dev2}},
				{Id: tag2, Name: "access-points", DeviceIds: []uuid.UUID{dev3}},
			}, nil),
			check: func(t *testing.T, got deviceTagsModel) {
				if len(got.DeviceTags) != 2 {
					t.Fatalf("device_tags = %d, want 2", len(got.DeviceTags))
				}
				first := got.DeviceTags[0]
				if first.ID.ValueString() != tag1.String() {
					t.Errorf("id = %q, want %q", first.ID.ValueString(), tag1.String())
				}
				if first.Name.ValueString() != "cameras" {
					t.Errorf("name = %q, want cameras", first.Name.ValueString())
				}
				var ids []string
				if diags := first.DeviceIDs.ElementsAs(context.Background(), &ids, false); diags.HasError() {
					t.Fatalf("device_ids elements: %v", diags)
				}
				wantIDs := []string{dev1.String(), dev2.String()}
				if len(ids) != len(wantIDs) {
					t.Fatalf("device_ids = %d, want %d", len(ids), len(wantIDs))
				}
				for i := range wantIDs {
					if ids[i] != wantIDs[i] {
						t.Errorf("device_ids[%d] = %q, want %q", i, ids[i], wantIDs[i])
					}
				}
				second := got.DeviceTags[1]
				if second.ID.ValueString() != tag2.String() {
					t.Errorf("second id = %q, want %q", second.ID.ValueString(), tag2.String())
				}
				if second.Name.ValueString() != "access-points" {
					t.Errorf("second name = %q, want access-points", second.Name.ValueString())
				}
				var secondIDs []string
				if diags := second.DeviceIDs.ElementsAs(context.Background(), &secondIDs, false); diags.HasError() {
					t.Fatalf("second device_ids elements: %v", diags)
				}
				if len(secondIDs) != 1 || secondIDs[0] != dev3.String() {
					t.Errorf("second device_ids = %v, want [%s]", secondIDs, dev3.String())
				}
			},
		},
		{
			name: "no tags yields empty state",
			seq:  seqOf([]official.DeviceTag{}, nil),
			check: func(t *testing.T, got deviceTagsModel) {
				if len(got.DeviceTags) != 0 {
					t.Errorf("device_tags = %d, want 0", len(got.DeviceTags))
				}
			},
		},
		{
			name:    "list error surfaces diagnostic",
			seq:     seqOf[official.DeviceTag](nil, errors.New("controller unreachable")),
			wantErr: "controller unreachable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSite uuid.UUID
			var gotFilter string
			oc := &official.ClientMock{
				SupportingFunc: func() official.SupportingClient {
					return &official.SupportingClientMock{
						ListDeviceTagsAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.DeviceTag, error] {
							gotSite = siteID
							gotFilter = filter
							return tt.seq
						},
					}
				},
			}
			ds := NewDeviceTagsDataSource().(*deviceTagsDataSource)
			state := configureDS(t, ds, oc)

			resp := datasource.ReadResponse{State: state}
			ds.Read(context.Background(), datasource.ReadRequest{}, &resp)

			if tt.wantErr != "" {
				if !hasErrorContaining(&resp, "Failed to list device tags") {
					t.Fatalf("expected list-device-tags diagnostic, got: %v", resp.Diagnostics)
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

			var got deviceTagsModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state get: %v", diags)
			}
			tt.check(t, got)
		})
	}
}
