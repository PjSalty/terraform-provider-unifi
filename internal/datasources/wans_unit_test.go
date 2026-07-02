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

func TestWansMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewWansDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_wans" {
		t.Errorf("type name = %q, want unifi_wans", resp.TypeName)
	}
}

func TestWansSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewWansDataSource().(*wansDataSource).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["wans"]; !ok {
		t.Error("schema missing wans attribute")
	}
}

func TestWansConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewWansDataSource().(*wansDataSource)
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
		ds := NewWansDataSource().(*wansDataSource)
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
		ds := NewWansDataSource().(*wansDataSource)
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

// wansMock builds an official.Client whose Supporting().ListWansAll returns seq.
func wansMock(seq func(ctx context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.WANOverview, error]) *official.ClientMock {
	return &official.ClientMock{
		SupportingFunc: func() official.SupportingClient {
			return &official.SupportingClientMock{ListWansAllFunc: seq}
		},
	}
}

func TestWansRead(t *testing.T) {
	primaryID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	backupID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	var gotSite uuid.UUID
	var gotFilter string
	oc := wansMock(func(_ context.Context, siteID uuid.UUID, filter string) iter.Seq2[official.WANOverview, error] {
		gotSite = siteID
		gotFilter = filter
		return seqOf([]official.WANOverview{
			{Id: primaryID, Name: "primary-wan"},
			{Id: backupID, Name: "backup-wan"},
		}, nil)
	})
	ds := NewWansDataSource().(*wansDataSource)
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

	var got wansModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Wans) != 2 {
		t.Fatalf("wans = %d, want 2", len(got.Wans))
	}
	first := got.Wans[0]
	if first.ID.ValueString() != primaryID.String() {
		t.Errorf("id = %q, want %q", first.ID.ValueString(), primaryID.String())
	}
	if first.Name.ValueString() != "primary-wan" {
		t.Errorf("name = %q, want primary-wan", first.Name.ValueString())
	}
	second := got.Wans[1]
	if second.ID.ValueString() != backupID.String() {
		t.Errorf("second id = %q, want %q", second.ID.ValueString(), backupID.String())
	}
	if second.Name.ValueString() != "backup-wan" {
		t.Errorf("second name = %q, want backup-wan", second.Name.ValueString())
	}
}

// TestWansReadListError proves an API failure mid-drain surfaces as a diagnostic
// instead of a partial state.
func TestWansReadListError(t *testing.T) {
	oc := wansMock(func(context.Context, uuid.UUID, string) iter.Seq2[official.WANOverview, error] {
		return seqOf[official.WANOverview](nil, errors.New("controller unreachable"))
	})
	ds := NewWansDataSource().(*wansDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if !hasErrorContaining(&resp, "Failed to list wans") {
		t.Fatalf("expected list-wans diagnostic, got: %v", resp.Diagnostics)
	}
	if !hasErrorContaining(&resp, "controller unreachable") {
		t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
	}
}

// TestWansReadEmpty proves an empty WAN list yields a successful Read with zero
// items rather than an error.
func TestWansReadEmpty(t *testing.T) {
	oc := wansMock(func(context.Context, uuid.UUID, string) iter.Seq2[official.WANOverview, error] {
		return seqOf([]official.WANOverview{}, nil)
	})
	ds := NewWansDataSource().(*wansDataSource)
	state := configureDS(t, ds, oc)

	resp := datasource.ReadResponse{State: state}
	ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	var got wansModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state get: %v", diags)
	}
	if len(got.Wans) != 0 {
		t.Fatalf("wans = %d, want 0", len(got.Wans))
	}
}
