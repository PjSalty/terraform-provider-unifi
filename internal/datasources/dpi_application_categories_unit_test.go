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

func TestDpiApplicationCategoriesMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewDpiApplicationCategoriesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_dpi_application_categories" {
		t.Errorf("type name = %q, want unifi_dpi_application_categories", resp.TypeName)
	}
}

func TestDpiApplicationCategoriesSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewDpiApplicationCategoriesDataSource().(*dpiApplicationCategoriesDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["dpi_application_categories"]; !ok {
		t.Error("schema missing dpi_application_categories attribute")
	}
}

func TestDpiApplicationCategoriesConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewDpiApplicationCategoriesDataSource().(*dpiApplicationCategoriesDataSource)
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
		ds := NewDpiApplicationCategoriesDataSource().(*dpiApplicationCategoriesDataSource)
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
		ds := NewDpiApplicationCategoriesDataSource().(*dpiApplicationCategoriesDataSource)
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

// dpiCategoriesSupportingMock wires a ListDpiApplicationCategoriesAll stub into
// the official client mock via the Supporting accessor.
func dpiCategoriesSupportingMock(list func(context.Context, string) iter.Seq2[official.DPICategory, error]) *official.ClientMock {
	return &official.ClientMock{
		SupportingFunc: func() official.SupportingClient {
			return &official.SupportingClientMock{ListDpiApplicationCategoriesAllFunc: list}
		},
	}
}

func TestDpiApplicationCategoriesRead(t *testing.T) {
	tests := []struct {
		name      string
		list      func(context.Context, string) iter.Seq2[official.DPICategory, error]
		wantErr   string
		wantCount int
	}{
		{
			name: "success maps every field",
			list: func(_ context.Context, filter string) iter.Seq2[official.DPICategory, error] {
				if filter != "" {
					t.Errorf("filter = %q, want empty (global list)", filter)
				}
				return seqOf([]official.DPICategory{
					{Id: 1, Name: "Web"},
					{Id: 2, Name: "Streaming Media"},
				}, nil)
			},
			wantCount: 2,
		},
		{
			name: "list error surfaces as diagnostic",
			list: func(context.Context, string) iter.Seq2[official.DPICategory, error] {
				return seqOf[official.DPICategory](nil, errors.New("boom"))
			},
			wantErr: "boom",
		},
		{
			name: "empty list yields zero items",
			list: func(context.Context, string) iter.Seq2[official.DPICategory, error] {
				return seqOf([]official.DPICategory{}, nil)
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds := NewDpiApplicationCategoriesDataSource().(*dpiApplicationCategoriesDataSource)
			state := configureDS(t, ds, dpiCategoriesSupportingMock(tc.list))

			resp := datasource.ReadResponse{State: state}
			ds.Read(context.Background(), datasource.ReadRequest{}, &resp)

			if tc.wantErr != "" {
				if !hasErrorContaining(&resp, "Failed to list DPI application categories") {
					t.Fatalf("expected list diagnostic, got: %v", resp.Diagnostics)
				}
				if !hasErrorContaining(&resp, tc.wantErr) {
					t.Errorf("diagnostic missing underlying error %q: %v", tc.wantErr, resp.Diagnostics)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("read diagnostics: %v", resp.Diagnostics)
			}

			var got dpiApplicationCategoriesModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state get: %v", diags)
			}
			if len(got.DpiApplicationCategories) != tc.wantCount {
				t.Fatalf("categories = %d, want %d", len(got.DpiApplicationCategories), tc.wantCount)
			}
			if tc.wantCount == 2 {
				first := got.DpiApplicationCategories[0]
				if first.ID.ValueInt64() != 1 {
					t.Errorf("id = %d, want 1", first.ID.ValueInt64())
				}
				if first.Name.ValueString() != "Web" {
					t.Errorf("name = %q, want Web", first.Name.ValueString())
				}
				second := got.DpiApplicationCategories[1]
				if second.ID.ValueInt64() != 2 {
					t.Errorf("second id = %d, want 2", second.ID.ValueInt64())
				}
				if second.Name.ValueString() != "Streaming Media" {
					t.Errorf("second name = %q, want Streaming Media", second.Name.ValueString())
				}
			}
		})
	}
}
