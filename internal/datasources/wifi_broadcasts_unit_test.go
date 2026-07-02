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

// wifiBroadcastsMock wires a WifiBroadcasts ListAll stub into a full client
// mock (the stub returns items then the trailing error, via seqOf).
func wifiBroadcastsMock(list func() ([]official.WifiBroadcastOverview, error)) official.Client {
	wc := &official.WifiBroadcastsClientMock{
		ListAllFunc: func(context.Context, uuid.UUID, string) iter.Seq2[official.WifiBroadcastOverview, error] {
			items, err := list()
			return seqOf(items, err)
		},
	}
	return &official.ClientMock{WifiBroadcastsFunc: func() official.WifiBroadcastsClient { return wc }}
}

func TestWifiBroadcastsMetadata(t *testing.T) {
	var resp datasource.MetadataResponse
	NewWifiBroadcastsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_wifi_broadcasts" {
		t.Errorf("type name = %q, want unifi_wifi_broadcasts", resp.TypeName)
	}
}

func TestWifiBroadcastsSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewWifiBroadcastsDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["wifi_broadcasts"]; !ok {
		t.Error("schema missing wifi_broadcasts attribute")
	}
}

func TestWifiBroadcastsConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
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
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
		var resp datasource.ConfigureResponse
		ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "nope"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
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

func TestWifiBroadcastsRead(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()

	t.Run("happy path maps every field", func(t *testing.T) {
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
		oc := wifiBroadcastsMock(func() ([]official.WifiBroadcastOverview, error) {
			return []official.WifiBroadcastOverview{
				{Id: id1, Name: "Byte me", Enabled: true, Type: "STANDARD",
					Metadata: official.UserOrDerivedOrOrchestratedEntityMetadata{Origin: "USER"}},
				{Id: id2, Name: "Salt Lair IoT", Enabled: false, Type: "STANDARD",
					Metadata: official.UserOrDerivedOrOrchestratedEntityMetadata{Origin: "USER"}},
			}, nil
		})
		state := configureDS(t, ds, oc)
		resp := datasource.ReadResponse{State: state}
		ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		var got wifiBroadcastsModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if len(got.WifiBroadcasts) != 2 {
			t.Fatalf("wifi_broadcasts = %d, want 2", len(got.WifiBroadcasts))
		}
		b := got.WifiBroadcasts[0]
		if b.ID.ValueString() != id1.String() || b.Name.ValueString() != "Byte me" ||
			!b.Enabled.ValueBool() || b.Type.ValueString() != "STANDARD" || b.Origin.ValueString() != "USER" {
			t.Errorf("first broadcast mapped wrong: %+v", b)
		}
		if got.WifiBroadcasts[1].Enabled.ValueBool() {
			t.Error("second broadcast enabled = true, want false")
		}
	})

	t.Run("list error surfaces", func(t *testing.T) {
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
		oc := wifiBroadcastsMock(func() ([]official.WifiBroadcastOverview, error) {
			return nil, errors.New("controller unreachable")
		})
		state := configureDS(t, ds, oc)
		resp := datasource.ReadResponse{State: state}
		ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
		if !hasErrorContaining(&resp, "controller unreachable") {
			t.Fatalf("expected list-error diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("empty list succeeds", func(t *testing.T) {
		ds := NewWifiBroadcastsDataSource().(*wifiBroadcastsDataSource)
		oc := wifiBroadcastsMock(func() ([]official.WifiBroadcastOverview, error) { return nil, nil })
		state := configureDS(t, ds, oc)
		resp := datasource.ReadResponse{State: state}
		ds.Read(context.Background(), datasource.ReadRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})
}
