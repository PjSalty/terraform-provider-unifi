package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// wbSchemaShell returns an empty (null-raw) state shell built from the
// wifi_broadcast schema, failing the test on any schema diagnostic.
func wbSchemaShell(t *testing.T) tfsdk.State {
	t.Helper()
	var resp resource.SchemaResponse
	NewWifiBroadcastResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	s := tfsdk.State{Schema: resp.Schema}
	s.RemoveResource(context.Background())
	return s
}

// wbRes returns a wifi_broadcast resource configured against the given
// WifiBroadcasts mock.
func wbRes(t *testing.T, wc *official.WifiBroadcastsClientMock, opts ...testutil.Opt) *wifiBroadcastResource {
	t.Helper()
	r := NewWifiBroadcastResource().(*wifiBroadcastResource)
	oc := &official.ClientMock{WifiBroadcastsFunc: func() official.WifiBroadcastsClient { return wc }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return r
}

// wbPlanOf builds a Plan carrying the given model.
func wbPlanOf(t *testing.T, m wifiBroadcastModel) tfsdk.Plan {
	t.Helper()
	shell := wbSchemaShell(t)
	p := tfsdk.Plan(shell)
	if diags := p.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("plan set: %v", diags)
	}
	return p
}

// wbStateOf builds a State carrying the given model.
func wbStateOf(t *testing.T, m wifiBroadcastModel) tfsdk.State {
	t.Helper()
	s := wbSchemaShell(t)
	if diags := s.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("state set: %v", diags)
	}
	return s
}

// wbToMap round-trips a body through JSON so assertions see exactly what the
// controller would receive.
func wbToMap(t *testing.T, body any) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// wbModel returns a fully-populated valid model with the given id.
func wbModel(id types.String) wifiBroadcastModel {
	return wifiBroadcastModel{
		ID:                  id,
		Name:                types.StringValue("guest-wifi"),
		Enabled:             types.BoolValue(true),
		HideName:            types.BoolValue(false),
		Security:            types.StringValue("WPA2_PERSONAL"),
		Passphrase:          types.StringValue("test-passphrase"),
		PmfMode:             types.StringNull(),
		Frequencies:         types.SetValueMust(types.StringType, []attr.Value{types.StringValue("5")}),
		NetworkID:           types.StringNull(),
		DeviceFilter:        types.SetNull(types.StringType),
		ClientIsolation:     types.BoolValue(false),
		ClientFilterMacs:    types.SetNull(types.StringType),
		BssTransition:       types.BoolValue(true),
		ArpProxy:            types.BoolValue(false),
		AdvertiseDeviceName: types.BoolValue(false),
	}
}

func TestWifiBroadcastMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	NewWifiBroadcastResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_wifi_broadcast" {
		t.Errorf("type name = %q, want unifi_wifi_broadcast", resp.TypeName)
	}
}

func TestWifiBroadcastSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewWifiBroadcastResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, a := range []string{
		"id", "name", "enabled", "hide_name", "security", "passphrase", "pmf_mode",
		"broadcasting_frequencies_ghz", "network_id", "broadcasting_device_filter",
		"client_isolation_enabled", "multicast_to_unicast_conversion_enabled",
		"uapsd_enabled", "client_filter_action", "client_filter_mac_addresses",
		"bss_transition_enabled", "arp_proxy_enabled", "advertise_device_name",
	} {
		if _, ok := resp.Schema.Attributes[a]; !ok {
			t.Errorf("schema missing %s attribute", a)
		}
	}
}

func TestWifiBroadcastConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		r := NewWifiBroadcastResource().(*wifiBroadcastResource)
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.data != nil {
			t.Error("data set from nil provider data")
		}
	})
	t.Run("wrong type errors", func(t *testing.T) {
		r := NewWifiBroadcastResource().(*wifiBroadcastResource)
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "nope"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for wrong provider data type")
		}
		if r.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		r := NewWifiBroadcastResource().(*wifiBroadcastResource)
		pd := testutil.Data(&official.ClientMock{})
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: pd}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.data != pd {
			t.Error("data not stored")
		}
	})
}

func TestWifiBroadcastCreate(t *testing.T) {
	planModel := wbModel(types.StringUnknown())

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{}, testutil.ReadOnly())
		resp := resource.CreateResponse{State: wbSchemaShell(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan surfaces diagnostics", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		shell := wbSchemaShell(t)
		resp := resource.CreateResponse{State: wbSchemaShell(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(shell)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null plan")
		}
	})

	t.Run("expand diagnostics stop before the api call", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		bad := planModel
		bad.NetworkID = types.StringValue("not-a-uuid")
		resp := resource.CreateResponse{State: wbSchemaShell(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: wbPlanOf(t, bad)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid network_id") {
			t.Fatalf("expected invalid-network_id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			CreateFunc: func(context.Context, uuid.UUID, official.WifiBroadcastCreateOrUpdate) (*official.WifiBroadcastDetails, error) {
				return nil, errors.New("controller rejected the ssid")
			},
		})
		resp := resource.CreateResponse{State: wbSchemaShell(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to create SSID") {
			t.Fatalf("expected create-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path sets state with returned id", func(t *testing.T) {
		newID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
		var gotSite uuid.UUID
		var gotBody official.WifiBroadcastCreateOrUpdate
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			CreateFunc: func(_ context.Context, siteID uuid.UUID, body official.WifiBroadcastCreateOrUpdate) (*official.WifiBroadcastDetails, error) {
				gotSite = siteID
				gotBody = body
				return &official.WifiBroadcastDetails{Id: newID}, nil
			},
		})
		resp := resource.CreateResponse{State: wbSchemaShell(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID {
			t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
		}
		if gotBody.Name != "guest-wifi" || gotBody.Type != "STANDARD" {
			t.Errorf("body = %+v, want guest-wifi/STANDARD", gotBody)
		}
		var got wifiBroadcastModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.ID.ValueString() != newID.String() {
			t.Errorf("id = %q, want %q", got.ID.ValueString(), newID.String())
		}
		if got.Passphrase.ValueString() != "test-passphrase" {
			t.Errorf("passphrase = %q, want preserved plan value", got.Passphrase.ValueString())
		}
	})
}

func TestWifiBroadcastRead(t *testing.T) {
	id := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	stateModel := wbModel(types.StringValue(id.String()))

	t.Run("bad state surfaces diagnostics", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		shell := wbSchemaShell(t)
		resp := resource.ReadResponse{State: wbSchemaShell(t)}
		r.Read(context.Background(), resource.ReadRequest{State: shell}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null state")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		bad := stateModel
		bad.ID = types.StringValue("not-a-uuid")
		st := wbStateOf(t, bad)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid SSID id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found removes resource", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.WifiBroadcastDetails, error) {
				return nil, fmt.Errorf("get ssid: %w", unifi.ErrNotFound)
			},
		})
		st := wbStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("state not removed for missing ssid")
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.WifiBroadcastDetails, error) {
				return nil, errors.New("controller unreachable")
			},
		})
		st := wbStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to read SSID") {
			t.Fatalf("expected read-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path refreshes top-level fields and keeps union fields", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			GetFunc: func(_ context.Context, siteID, ssidID uuid.UUID) (*official.WifiBroadcastDetails, error) {
				gotSite = siteID
				gotID = ssidID
				d := &official.WifiBroadcastDetails{
					Id:                     ssidID,
					Name:                   "guest-wifi-renamed",
					Enabled:                false,
					HideName:               true,
					ClientIsolationEnabled: true,
				}
				// Controller values differ from stateModel (bss true, arp false, adv false)
				// so a successful refresh flips all three.
				bssOff, arpOn, advOn := false, true, true
				if err := d.FromStandardWifiBroadcastDetail(official.StandardWifiBroadcastDetail{
					Id: ssidID, Type: "STANDARD",
					BssTransitionEnabled: &bssOff, ArpProxyEnabled: &arpOn, AdvertiseDeviceName: &advOn,
				}); err != nil {
					t.Fatalf("build standard detail: %v", err)
				}
				return d, nil
			},
		})
		st := wbStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID || gotID != id {
			t.Errorf("called with site %s id %s, want %s %s", gotSite, gotID, testutil.SiteID, id)
		}
		var got wifiBroadcastModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Name.ValueString() != "guest-wifi-renamed" || got.Enabled.ValueBool() ||
			!got.HideName.ValueBool() || !got.ClientIsolation.ValueBool() {
			t.Errorf("refreshed fields = %+v, want renamed/disabled/hidden/isolated", got)
		}
		// Union-backed fields stay from prior state (write-only or not refreshed).
		if got.Security.ValueString() != "WPA2_PERSONAL" {
			t.Errorf("security = %q, want WPA2_PERSONAL preserved", got.Security.ValueString())
		}
		if got.Passphrase.ValueString() != "test-passphrase" {
			t.Errorf("passphrase = %q, want preserved", got.Passphrase.ValueString())
		}
		// The three STANDARD-variant bools refresh from the controller.
		if got.BssTransition.ValueBool() || !got.ArpProxy.ValueBool() || !got.AdvertiseDeviceName.ValueBool() {
			t.Errorf("required bools = bss:%v arp:%v adv:%v, want false/true/true",
				got.BssTransition.ValueBool(), got.ArpProxy.ValueBool(), got.AdvertiseDeviceName.ValueBool())
		}
	})
}

func TestWifiBroadcastUpdate(t *testing.T) {
	id := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	planModel := wbModel(types.StringValue(id.String()))

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{}, testutil.ReadOnly())
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan surfaces diagnostics", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		shell := wbSchemaShell(t)
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: tfsdk.Plan(shell)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null plan")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		bad := planModel
		bad.ID = types.StringValue("not-a-uuid")
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: wbPlanOf(t, bad)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid SSID id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("expand diagnostics stop before the api call", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		bad := planModel
		bad.NetworkID = types.StringValue("not-a-uuid")
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: wbPlanOf(t, bad)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid network_id") {
			t.Fatalf("expected invalid-network_id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			UpdateFunc: func(context.Context, uuid.UUID, uuid.UUID, official.WifiBroadcastCreateOrUpdate) (*official.WifiBroadcastDetails, error) {
				return nil, errors.New("controller rejected the update")
			},
		})
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to update SSID") {
			t.Fatalf("expected update-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path sets state", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		var gotBody official.WifiBroadcastCreateOrUpdate
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			UpdateFunc: func(_ context.Context, siteID, ssidID uuid.UUID, body official.WifiBroadcastCreateOrUpdate) (*official.WifiBroadcastDetails, error) {
				gotSite = siteID
				gotID = ssidID
				gotBody = body
				return &official.WifiBroadcastDetails{Id: ssidID}, nil
			},
		})
		resp := resource.UpdateResponse{State: wbSchemaShell(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: wbPlanOf(t, planModel)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID || gotID != id {
			t.Errorf("called with site %s id %s, want %s %s", gotSite, gotID, testutil.SiteID, id)
		}
		if gotBody.Name != "guest-wifi" {
			t.Errorf("body name = %q, want guest-wifi", gotBody.Name)
		}
		var got wifiBroadcastModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.ID.ValueString() != id.String() {
			t.Errorf("id = %q, want %q", got.ID.ValueString(), id.String())
		}
	})
}

func TestWifiBroadcastDelete(t *testing.T) {
	id := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	stateModel := wbModel(types.StringValue(id.String()))

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{}, testutil.ReadOnly())
		st := wbStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("destroy protection blocks", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{}, testutil.DestroyProtection())
		st := wbStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad state surfaces diagnostics", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		shell := wbSchemaShell(t)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: shell}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null state")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{})
		bad := stateModel
		bad.ID = types.StringValue("not-a-uuid")
		st := wbStateOf(t, bad)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid SSID id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID, *official.DeleteWifiBroadcastOptions) error {
				return errors.New("controller refused")
			},
		})
		st := wbStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to delete SSID") {
			t.Fatalf("expected delete-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found is success", func(t *testing.T) {
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID, *official.DeleteWifiBroadcastOptions) error {
				return fmt.Errorf("delete ssid: %w", unifi.ErrNotFound)
			},
		})
		st := wbStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics for already-deleted ssid: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path deletes", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		r := wbRes(t, &official.WifiBroadcastsClientMock{
			DeleteFunc: func(_ context.Context, siteID, ssidID uuid.UUID, _ *official.DeleteWifiBroadcastOptions) error {
				gotSite = siteID
				gotID = ssidID
				return nil
			},
		})
		st := wbStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("delete diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID || gotID != id {
			t.Errorf("called with site %s id %s, want %s %s", gotSite, gotID, testutil.SiteID, id)
		}
	})
}

func TestWifiBroadcastImportState(t *testing.T) {
	r := NewWifiBroadcastResource().(*wifiBroadcastResource)
	resp := resource.ImportStateResponse{State: wbSchemaShell(t)}
	id := "55555555-5555-4555-8555-555555555555"
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}
	var got types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &got); diags.HasError() {
		t.Fatalf("state get attribute: %v", diags)
	}
	if got.ValueString() != id {
		t.Errorf("imported id = %q, want %q", got.ValueString(), id)
	}
}

// TestExpandWifiBroadcastDeviceFilter proves the DEVICES filter rides into the
// body when AP UUIDs are given.
func TestExpandWifiBroadcastDeviceFilter(t *testing.T) {
	apID := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	m := wbModel(types.StringNull())
	m.DeviceFilter = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(apID.String())})

	body, diags := expandWifiBroadcast(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := wbToMap(t, body)
	filter, ok := got["broadcastingDeviceFilter"].(map[string]any)
	if !ok {
		t.Fatalf("broadcastingDeviceFilter not an object: %v", got["broadcastingDeviceFilter"])
	}
	if filter["type"] != "DEVICES" {
		t.Errorf("filter type = %v, want DEVICES", filter["type"])
	}
	ids, ok := filter["deviceIds"].([]any)
	if !ok || len(ids) != 1 || ids[0] != apID.String() {
		t.Errorf("deviceIds = %v, want [%s]", filter["deviceIds"], apID.String())
	}
}

// TestExpandWifiBroadcastInvalidNetworkID covers the network_id parse-failure
// diagnostic.
func TestExpandWifiBroadcastInvalidNetworkID(t *testing.T) {
	m := wbModel(types.StringNull())
	m.NetworkID = types.StringValue("not-a-uuid")
	_, diags := expandWifiBroadcast(context.Background(), m)
	if !netDiagsContain(diags, "Invalid network_id") {
		t.Fatalf("expected invalid-network_id diagnostic, got: %v", diags)
	}
}

// TestExpandWifiBroadcastAllBands covers the 6 GHz band mapping alongside the
// other two.
func TestExpandWifiBroadcastAllBands(t *testing.T) {
	m := wbModel(types.StringNull())
	m.Frequencies = types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("2.4"), types.StringValue("5"), types.StringValue("6"),
	})
	body, diags := expandWifiBroadcast(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := wbToMap(t, body)
	bands, ok := got["broadcastingFrequenciesGHz"].([]any)
	if !ok || len(bands) != 3 {
		t.Fatalf("broadcastingFrequenciesGHz = %v, want 3 bands", got["broadcastingFrequenciesGHz"])
	}
}

// TestExpandWifiBroadcastToggles proves the two plain-bool QoL fields ride into
// the body as top-level keys.
func TestExpandWifiBroadcastToggles(t *testing.T) {
	m := wbModel(types.StringNull())
	m.MulticastToUnicast = types.BoolValue(true)
	m.UapsdEnabled = types.BoolValue(true)
	body, diags := expandWifiBroadcast(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := wbToMap(t, body)
	if got["multicastToUnicastConversionEnabled"] != true {
		t.Errorf("multicastToUnicastConversionEnabled = %v, want true", got["multicastToUnicastConversionEnabled"])
	}
	if got["uapsdEnabled"] != true {
		t.Errorf("uapsdEnabled = %v, want true", got["uapsdEnabled"])
	}
}

// TestExpandWifiBroadcastRequiredBools proves the three controller-required bools
// (which the API rejects as null) always ride into the body as top-level keys.
func TestExpandWifiBroadcastRequiredBools(t *testing.T) {
	m := wbModel(types.StringNull())
	m.BssTransition = types.BoolValue(true)
	m.ArpProxy = types.BoolValue(true)
	m.AdvertiseDeviceName = types.BoolValue(true)
	body, diags := expandWifiBroadcast(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := wbToMap(t, body)
	for _, k := range []string{"bssTransitionEnabled", "arpProxyEnabled", "advertiseDeviceName"} {
		v, present := got[k]
		if !present {
			t.Errorf("%s missing from body (the controller rejects null)", k)
			continue
		}
		if v != true {
			t.Errorf("%s = %v, want true", k, v)
		}
	}
}

// TestExpandWifiBroadcastClientFilter proves the MAC allow/deny list builds the
// clientFilteringPolicy object, and that MACs without an action are rejected.
func TestExpandWifiBroadcastClientFilter(t *testing.T) {
	t.Run("allow list builds the policy", func(t *testing.T) {
		m := wbModel(types.StringNull())
		m.ClientFilterAction = types.StringValue("ALLOW")
		m.ClientFilterMacs = types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("aa:bb:cc:dd:ee:ff"), types.StringValue("11:22:33:44:55:66"),
		})
		body, diags := expandWifiBroadcast(context.Background(), m)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		got := wbToMap(t, body)
		pol, ok := got["clientFilteringPolicy"].(map[string]any)
		if !ok {
			t.Fatalf("clientFilteringPolicy not an object: %v", got["clientFilteringPolicy"])
		}
		if pol["action"] != "ALLOW" {
			t.Errorf("action = %v, want ALLOW", pol["action"])
		}
		macs, ok := pol["macAddressFilter"].([]any)
		if !ok || len(macs) != 2 {
			t.Errorf("macAddressFilter = %v, want 2 entries", pol["macAddressFilter"])
		}
	})

	t.Run("macs without action error", func(t *testing.T) {
		m := wbModel(types.StringNull())
		m.ClientFilterMacs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("aa:bb:cc:dd:ee:ff")})
		_, diags := expandWifiBroadcast(context.Background(), m)
		if !netDiagsContain(diags, "requires client_filter_action") {
			t.Fatalf("expected macs-without-action diagnostic, got: %v", diags)
		}
	})
}

func TestExpandSecurityWPA3(t *testing.T) {
	sec, diags := expandSecurity(wifiBroadcastModel{
		Security:   types.StringValue("WPA3_PERSONAL"),
		Passphrase: types.StringValue("test-passphrase"),
		PmfMode:    types.StringNull(),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := wbToMap(t, sec)
	if got["type"] != "WPA3_PERSONAL" {
		t.Errorf("type = %v, want WPA3_PERSONAL", got["type"])
	}
	if got["passphrase"] != "test-passphrase" {
		t.Errorf("passphrase = %v, want test-passphrase", got["passphrase"])
	}
}

func TestExpandSecurityWPA2WPA3(t *testing.T) {
	t.Run("with pmf mode", func(t *testing.T) {
		sec, diags := expandSecurity(wifiBroadcastModel{
			Security:   types.StringValue("WPA2_WPA3_PERSONAL"),
			Passphrase: types.StringValue("test-passphrase"),
			PmfMode:    types.StringValue("REQUIRED"),
		})
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		got := wbToMap(t, sec)
		if got["type"] != "WPA2_WPA3_PERSONAL" {
			t.Errorf("type = %v, want WPA2_WPA3_PERSONAL", got["type"])
		}
		if got["pmfMode"] != "REQUIRED" {
			t.Errorf("pmfMode = %v, want REQUIRED", got["pmfMode"])
		}
	})
	t.Run("without pmf mode", func(t *testing.T) {
		sec, diags := expandSecurity(wifiBroadcastModel{
			Security:   types.StringValue("WPA2_WPA3_PERSONAL"),
			Passphrase: types.StringValue("test-passphrase"),
			PmfMode:    types.StringNull(),
		})
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		got := wbToMap(t, sec)
		if _, present := got["pmfMode"]; present {
			t.Errorf("pmfMode should be omitted when null, got %v", got["pmfMode"])
		}
	})
}

// TestExpandSecurityUnsupported covers the default branch. The schema
// validator normally rejects this value; the expander still guards it.
func TestExpandSecurityUnsupported(t *testing.T) {
	_, diags := expandSecurity(wifiBroadcastModel{
		Security:   types.StringValue("WEP"),
		Passphrase: types.StringNull(),
		PmfMode:    types.StringNull(),
	})
	if !netDiagsContain(diags, "Unsupported security mode") {
		t.Fatalf("expected unsupported-mode diagnostic, got: %v", diags)
	}
}

func TestSetToUUIDs(t *testing.T) {
	ctx := context.Background()
	t.Run("null set returns nil", func(t *testing.T) {
		var diags diag.Diagnostics
		if got := setToUUIDs(ctx, types.SetNull(types.StringType), &diags); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("unknown set returns nil", func(t *testing.T) {
		var diags diag.Diagnostics
		if got := setToUUIDs(ctx, types.SetUnknown(types.StringType), &diags); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("invalid uuid errors and is skipped", func(t *testing.T) {
		var diags diag.Diagnostics
		valid := uuid.MustParse("77777777-7777-4777-8777-777777777777")
		s := types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("not-a-uuid"), types.StringValue(valid.String()),
		})
		got := setToUUIDs(ctx, s, &diags)
		if !netDiagsContain(diags, "Invalid UUID") {
			t.Fatalf("expected invalid-uuid diagnostic, got: %v", diags)
		}
		if len(got) != 1 || got[0] != valid {
			t.Errorf("got %v, want just %s", got, valid)
		}
	})
}

func TestSetToFrequencies(t *testing.T) {
	ctx := context.Background()
	t.Run("null set returns nil", func(t *testing.T) {
		var diags diag.Diagnostics
		if got := setToFrequencies(ctx, types.SetNull(types.StringType), &diags); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("unknown set returns nil", func(t *testing.T) {
		var diags diag.Diagnostics
		if got := setToFrequencies(ctx, types.SetUnknown(types.StringType), &diags); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("invalid band errors", func(t *testing.T) {
		var diags diag.Diagnostics
		s := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("7")})
		if got := setToFrequencies(ctx, s, &diags); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
		if !netDiagsContain(diags, "Invalid frequency") {
			t.Fatalf("expected invalid-frequency diagnostic, got: %v", diags)
		}
	})
}
