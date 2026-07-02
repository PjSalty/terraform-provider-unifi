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

func TestRadiusProfilesMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewRadiusProfilesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_radius_profiles" {
		t.Errorf("type name = %q, want unifi_radius_profiles", resp.TypeName)
	}
}

func TestRadiusProfilesSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewRadiusProfilesDataSource().(*radiusProfilesDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["radius_profiles"]; !ok {
		t.Error("schema missing radius_profiles attribute")
	}
}

func TestRadiusProfilesConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
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
		ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
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
		ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
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

// radiusProfilesClient wraps a SupportingClientMock in the ClientMock accessor
// shape the data source calls: Official().Supporting().ListRadiusProfilesAll.
func radiusProfilesClient(mock *official.SupportingClientMock) *official.ClientMock {
	return &official.ClientMock{
		SupportingFunc: func() official.SupportingClient { return mock },
	}
}

func TestRadiusProfilesRead(t *testing.T) {
	corpID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	guestID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	var gotSite uuid.UUID
	var gotFilter string
	oc := radiusProfilesClient(&official.SupportingClientMock{
		ListRadiusProfilesAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.RadiusProfileOverview, error] {
			gotSite = siteID
			gotFilter = filter
			return seqOf([]official.RadiusProfileOverview{
				{Id: corpID, Name: "corp-radius"},
				{Id: guestID, Name: "guest-radius"},
			}, nil)
		},
	})
	ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
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

	var got radiusProfilesModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.RadiusProfiles) != 2 {
		t.Fatalf("radius_profiles = %d, want 2", len(got.RadiusProfiles))
	}
	first := got.RadiusProfiles[0]
	if first.ID.ValueString() != corpID.String() {
		t.Errorf("id = %q, want %q", first.ID.ValueString(), corpID.String())
	}
	if first.Name.ValueString() != "corp-radius" {
		t.Errorf("name = %q, want corp-radius", first.Name.ValueString())
	}
	second := got.RadiusProfiles[1]
	if second.ID.ValueString() != guestID.String() {
		t.Errorf("second id = %q, want %q", second.ID.ValueString(), guestID.String())
	}
	if second.Name.ValueString() != "guest-radius" {
		t.Errorf("second name = %q, want guest-radius", second.Name.ValueString())
	}
}

// TestRadiusProfilesReadListError proves an API failure mid-drain surfaces as a
// diagnostic instead of a partial state.
func TestRadiusProfilesReadListError(t *testing.T) {
	oc := radiusProfilesClient(&official.SupportingClientMock{
		ListRadiusProfilesAllFunc: func(context.Context, uuid.UUID, string) iter.Seq2[official.RadiusProfileOverview, error] {
			return seqOf[official.RadiusProfileOverview](nil, errors.New("controller unreachable"))
		},
	})
	ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to list RADIUS profiles") {
		t.Fatalf("expected list-radius-profiles diagnostic, got: %v", resp.Diagnostics)
	}
	if !hasErrorContaining(&resp, "controller unreachable") {
		t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
	}
}

// TestRadiusProfilesReadEmpty proves an empty list yields a successful Read with
// zero items.
func TestRadiusProfilesReadEmpty(t *testing.T) {
	oc := radiusProfilesClient(&official.SupportingClientMock{
		ListRadiusProfilesAllFunc: func(context.Context, uuid.UUID, string) iter.Seq2[official.RadiusProfileOverview, error] {
			return seqOf([]official.RadiusProfileOverview{}, nil)
		},
	})
	ds := NewRadiusProfilesDataSource().(*radiusProfilesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}

	var got radiusProfilesModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.RadiusProfiles) != 0 {
		t.Fatalf("radius_profiles = %d, want 0", len(got.RadiusProfiles))
	}
}
