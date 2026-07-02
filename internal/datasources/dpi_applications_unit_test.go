package datasources

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

func TestDPIApplicationsMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewDPIApplicationsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_dpi_applications" {
		t.Errorf("type name = %q, want unifi_dpi_applications", resp.TypeName)
	}
}

func TestDPIApplicationsSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewDPIApplicationsDataSource().(*dpiApplicationsDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["dpi_applications"]; !ok {
		t.Error("schema missing dpi_applications attribute")
	}
}

func TestDPIApplicationsConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewDPIApplicationsDataSource().(*dpiApplicationsDataSource)
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
		ds := NewDPIApplicationsDataSource().(*dpiApplicationsDataSource)
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
		ds := NewDPIApplicationsDataSource().(*dpiApplicationsDataSource)
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

// dpiApplicationsMock wraps a SupportingClientMock as the official.Client the
// data source resolves through c.Official().Supporting().
func dpiApplicationsMock(list func(context.Context, string) iter.Seq2[official.DPIApplication, error]) official.Client {
	return &official.ClientMock{
		SupportingFunc: func() official.SupportingClient {
			return &official.SupportingClientMock{ListDpiApplicationsAllFunc: list}
		},
	}
}

func TestDPIApplicationsRead(t *testing.T) {
	tests := []struct {
		name      string
		list      func(context.Context, string) iter.Seq2[official.DPIApplication, error]
		wantErr   string
		wantCount int
	}{
		{
			name: "success maps every field",
			list: func(_ context.Context, filter string) iter.Seq2[official.DPIApplication, error] {
				if filter != "" {
					t.Errorf("filter = %q, want empty (global list, no siteId)", filter)
				}
				return seqOf([]official.DPIApplication{
					{Id: 5, Name: "Netflix"},
					{Id: 42, Name: "BitTorrent"},
				}, nil)
			},
			wantCount: 2,
		},
		{
			name: "list error surfaces as diagnostic",
			list: func(context.Context, string) iter.Seq2[official.DPIApplication, error] {
				return seqOf[official.DPIApplication](nil, errors.New("boom"))
			},
			wantErr: "boom",
		},
		{
			name: "empty list yields zero items",
			list: func(context.Context, string) iter.Seq2[official.DPIApplication, error] {
				return seqOf([]official.DPIApplication{}, nil)
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := dpiApplicationsMock(tt.list)
			ds := NewDPIApplicationsDataSource().(*dpiApplicationsDataSource)
			state := configureDS(t, ds, oc)

			resp := datasource.ReadResponse{State: state}
			ds.Read(context.Background(), datasource.ReadRequest{}, &resp)

			if tt.wantErr != "" {
				if !hasErrorContaining(&resp, "Failed to list DPI applications") {
					t.Fatalf("expected list-failure diagnostic, got: %v", resp.Diagnostics)
				}
				if !hasErrorContaining(&resp, tt.wantErr) {
					t.Errorf("diagnostic missing underlying error %q: %v", tt.wantErr, resp.Diagnostics)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("read diagnostics: %v", resp.Diagnostics)
			}

			var got dpiApplicationsModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state get: %v", diags)
			}
			if len(got.DPIApplications) != tt.wantCount {
				t.Fatalf("dpi_applications = %d, want %d", len(got.DPIApplications), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			first := got.DPIApplications[0]
			if first.ID.ValueInt64() != 5 {
				t.Errorf("id = %d, want 5", first.ID.ValueInt64())
			}
			if first.Name.ValueString() != "Netflix" {
				t.Errorf("name = %q, want Netflix", first.Name.ValueString())
			}
			second := got.DPIApplications[1]
			if second.ID.ValueInt64() != 42 {
				t.Errorf("second id = %d, want 42", second.ID.ValueInt64())
			}
			if second.Name.ValueString() != "BitTorrent" {
				t.Errorf("second name = %q, want BitTorrent", second.Name.ValueString())
			}
		})
	}
}
