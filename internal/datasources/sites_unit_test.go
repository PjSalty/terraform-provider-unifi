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

func TestSitesMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewSitesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_sites" {
		t.Errorf("type name = %q, want unifi_sites", resp.TypeName)
	}
}

func TestSitesSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewSitesDataSource().(*sitesDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["sites"]; !ok {
		t.Error("schema missing sites attribute")
	}
}

func TestSitesConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewSitesDataSource().(*sitesDataSource)
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
		ds := NewSitesDataSource().(*sitesDataSource)
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
		ds := NewSitesDataSource().(*sitesDataSource)
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

func TestSitesRead(t *testing.T) {
	defaultID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	remoteID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	var gotFilter string
	oc := &official.ClientMock{
		SitesFunc: func() official.SitesClient {
			return &official.SitesClientMock{
				ListAllFunc: func(_ context.Context, filter string) iter.Seq2[official.SiteOverview, error] {
					gotFilter = filter
					return seqOf([]official.SiteOverview{
						{ID: defaultID, Name: "Default", InternalReference: "default"},
						{ID: remoteID, Name: "Remote Cabin", InternalReference: "remote"},
					}, nil)
				},
			}
		},
	}
	ds := NewSitesDataSource().(*sitesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if gotFilter != "" {
		t.Errorf("filter = %q, want empty", gotFilter)
	}

	var got sitesModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Sites) != 2 {
		t.Fatalf("sites = %d, want 2", len(got.Sites))
	}
	first := got.Sites[0]
	if first.ID.ValueString() != defaultID.String() {
		t.Errorf("id = %q, want %q", first.ID.ValueString(), defaultID.String())
	}
	if first.Name.ValueString() != "Default" {
		t.Errorf("name = %q, want Default", first.Name.ValueString())
	}
	if first.InternalReference.ValueString() != "default" {
		t.Errorf("internal_reference = %q, want default", first.InternalReference.ValueString())
	}
	second := got.Sites[1]
	if second.ID.ValueString() != remoteID.String() {
		t.Errorf("second id = %q, want %q", second.ID.ValueString(), remoteID.String())
	}
	if second.Name.ValueString() != "Remote Cabin" {
		t.Errorf("second name = %q, want Remote Cabin", second.Name.ValueString())
	}
	if second.InternalReference.ValueString() != "remote" {
		t.Errorf("second internal_reference = %q, want remote", second.InternalReference.ValueString())
	}
}

// TestSitesReadListError proves an API failure mid-drain surfaces as a
// diagnostic instead of a partial state.
func TestSitesReadListError(t *testing.T) {
	oc := &official.ClientMock{
		SitesFunc: func() official.SitesClient {
			return &official.SitesClientMock{
				ListAllFunc: func(context.Context, string) iter.Seq2[official.SiteOverview, error] {
					return seqOf[official.SiteOverview](nil, errors.New("controller unreachable"))
				},
			}
		},
	}
	ds := NewSitesDataSource().(*sitesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to list sites") {
		t.Fatalf("expected list-sites diagnostic, got: %v", resp.Diagnostics)
	}
	if !hasErrorContaining(&resp, "controller unreachable") {
		t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
	}
}

// TestSitesReadEmpty proves an empty site list is a successful Read with zero
// items, not an error.
func TestSitesReadEmpty(t *testing.T) {
	oc := &official.ClientMock{
		SitesFunc: func() official.SitesClient {
			return &official.SitesClientMock{
				ListAllFunc: func(context.Context, string) iter.Seq2[official.SiteOverview, error] {
					return seqOf([]official.SiteOverview{}, nil)
				},
			}
		},
	}
	ds := NewSitesDataSource().(*sitesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}

	var got sitesModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Sites) != 0 {
		t.Fatalf("sites = %d, want 0", len(got.Sites))
	}
}
