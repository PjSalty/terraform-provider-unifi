package datasources

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// seqOf yields items with nil errors, then the trailing error if non-nil. It is
// the test-side stand-in for the lazy paging iterators the go-unifi List*All
// methods return.
func seqOf[T any](items []T, trailing error) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, it := range items {
			if !yield(it, nil) {
				return
			}
		}
		if trailing != nil {
			var zero T
			yield(zero, trailing)
		}
	}
}

// configureDS runs Configure with the harness provider data and fails the test
// on any diagnostic, returning a State shell built from the data source's own
// schema so Read has somewhere to write.
func configureDS(t *testing.T, ds datasource.DataSourceWithConfigure, oc official.Client) tfsdk.State {
	t.Helper()
	var cfgResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: testutil.Data(oc)}, &cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", cfgResp.Diagnostics)
	}
	var schemaResp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	return tfsdk.State{Schema: schemaResp.Schema}
}

// hasErrorContaining reports whether any error diagnostic's summary or detail
// contains want.
func hasErrorContaining(resp *datasource.ReadResponse, want string) bool {
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Summary(), want) || strings.Contains(d.Detail(), want) {
			return true
		}
	}
	return false
}

func TestDevicesMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewDevicesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_devices" {
		t.Errorf("type name = %q, want unifi_devices", resp.TypeName)
	}
}

func TestDevicesSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewDevicesDataSource().(*devicesDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["devices"]; !ok {
		t.Error("schema missing devices attribute")
	}
}

func TestDevicesConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewDevicesDataSource().(*devicesDataSource)
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
		ds := NewDevicesDataSource().(*devicesDataSource)
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
		ds := NewDevicesDataSource().(*devicesDataSource)
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

func TestDevicesRead(t *testing.T) {
	apID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	swID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	var gotSite uuid.UUID
	var gotFilter string
	oc := &official.ClientMock{
		DevicesFunc: func() official.DevicesClient {
			return &official.DevicesClientMock{
				ListAdoptedAllFunc: func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.AdoptedDeviceOverview, error] {
					gotSite = siteID
					gotFilter = filter
					return seqOf([]official.AdoptedDeviceOverview{
						{Id: apID, Name: "office-ap", MacAddress: "00:11:22:33:44:55", Model: "U6-Lite", IpAddress: "192.0.2.30", State: "ONLINE"},
						{Id: swID, Name: "rack-switch", MacAddress: "66:77:88:99:aa:bb", Model: "USW-24", IpAddress: "192.0.2.31", State: "OFFLINE"},
					}, nil)
				},
			}
		},
	}
	ds := NewDevicesDataSource().(*devicesDataSource)
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

	var got devicesModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(got.Devices))
	}
	first := got.Devices[0]
	if first.ID.ValueString() != apID.String() {
		t.Errorf("id = %q, want %q", first.ID.ValueString(), apID.String())
	}
	if first.Name.ValueString() != "office-ap" {
		t.Errorf("name = %q, want office-ap", first.Name.ValueString())
	}
	if first.MacAddress.ValueString() != "00:11:22:33:44:55" {
		t.Errorf("mac_address = %q, want 00:11:22:33:44:55", first.MacAddress.ValueString())
	}
	if first.Model.ValueString() != "U6-Lite" {
		t.Errorf("model = %q, want U6-Lite", first.Model.ValueString())
	}
	if first.IPAddress.ValueString() != "192.0.2.30" {
		t.Errorf("ip_address = %q, want 192.0.2.30", first.IPAddress.ValueString())
	}
	if first.State.ValueString() != "ONLINE" {
		t.Errorf("state = %q, want ONLINE", first.State.ValueString())
	}
	if got.Devices[1].State.ValueString() != "OFFLINE" {
		t.Errorf("second state = %q, want OFFLINE", got.Devices[1].State.ValueString())
	}
}

// TestDevicesReadListError proves an API failure mid-drain surfaces as a
// diagnostic instead of a partial state.
func TestDevicesReadListError(t *testing.T) {
	oc := &official.ClientMock{
		DevicesFunc: func() official.DevicesClient {
			return &official.DevicesClientMock{
				ListAdoptedAllFunc: func(context.Context, uuid.UUID, string) iter.Seq2[official.AdoptedDeviceOverview, error] {
					return seqOf[official.AdoptedDeviceOverview](nil, errors.New("controller unreachable"))
				},
			}
		},
	}
	ds := NewDevicesDataSource().(*devicesDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to list devices") {
		t.Fatalf("expected list-devices diagnostic, got: %v", resp.Diagnostics)
	}
	if !hasErrorContaining(&resp, "controller unreachable") {
		t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
	}
}
