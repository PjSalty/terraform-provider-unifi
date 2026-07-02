package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// netDiagsContain reports whether any error diagnostic's summary or detail
// contains want.
func netDiagsContain(diags diag.Diagnostics, want string) bool {
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), want) || strings.Contains(d.Detail(), want) {
			return true
		}
	}
	return false
}

// netSchemaForTest returns the network resource schema, failing the test on
// any schema diagnostic.
func netSchemaForTest(t *testing.T) tfsdk.State {
	t.Helper()
	var resp resource.SchemaResponse
	NewNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	// RemoveResource initializes Raw to the schema's null object, which is
	// exactly the empty-state shell CRUD responses start from.
	s := tfsdk.State{Schema: resp.Schema}
	s.RemoveResource(context.Background())
	return s
}

// netRes returns a network resource configured against the given Networks
// mock.
func netRes(t *testing.T, nc *official.NetworksClientMock, opts ...testutil.Opt) *networkResource {
	t.Helper()
	r := NewNetworkResource().(*networkResource)
	oc := &official.ClientMock{NetworksFunc: func() official.NetworksClient { return nc }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return r
}

// netPlanOf builds a Plan carrying the given model.
func netPlanOf(t *testing.T, m networkModel) tfsdk.Plan {
	t.Helper()
	shell := netSchemaForTest(t)
	p := tfsdk.Plan{Schema: shell.Schema, Raw: shell.Raw}
	if diags := p.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("plan set: %v", diags)
	}
	return p
}

// netStateOf builds a State carrying the given model.
func netStateOf(t *testing.T, m networkModel) tfsdk.State {
	t.Helper()
	s := netSchemaForTest(t)
	if diags := s.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("state set: %v", diags)
	}
	return s
}

func TestNetworkMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	NewNetworkResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_network" {
		t.Errorf("type name = %q, want unifi_network", resp.TypeName)
	}
}

func TestNetworkSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, attr := range []string{"id", "name", "vlan_id", "enabled"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("schema missing %s attribute", attr)
		}
	}
}

func TestNetworkConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		r := NewNetworkResource().(*networkResource)
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
		r := NewNetworkResource().(*networkResource)
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for wrong provider data type")
		}
		if r.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		r := NewNetworkResource().(*networkResource)
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

func TestNetworkCreate(t *testing.T) {
	planModel := networkModel{
		ID:      types.StringUnknown(),
		Name:    types.StringValue("iot"),
		VlanID:  types.Int64Value(42),
		Enabled: types.BoolValue(true),
	}

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{}, testutil.ReadOnly())
		resp := resource.CreateResponse{State: netSchemaForTest(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan surfaces diagnostics", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		// A null plan cannot be reified into the model struct.
		shell := netSchemaForTest(t)
		resp := resource.CreateResponse{State: netSchemaForTest(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: shell.Schema, Raw: shell.Raw}}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null plan")
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			CreateFunc: func(context.Context, uuid.UUID, official.NetworkCreateOrUpdate) (*official.NetworkDetails, error) {
				return nil, errors.New("controller rejected the network")
			},
		})
		resp := resource.CreateResponse{State: netSchemaForTest(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to create network") {
			t.Fatalf("expected create-failure diagnostic, got: %v", resp.Diagnostics)
		}
		if !netDiagsContain(resp.Diagnostics, "controller rejected the network") {
			t.Errorf("diagnostic missing underlying error: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path sets state", func(t *testing.T) {
		newID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
		var gotSite uuid.UUID
		var gotBody official.NetworkCreateOrUpdate
		r := netRes(t, &official.NetworksClientMock{
			CreateFunc: func(_ context.Context, siteID uuid.UUID, body official.NetworkCreateOrUpdate) (*official.NetworkDetails, error) {
				gotSite = siteID
				gotBody = body
				return &official.NetworkDetails{Id: newID, Name: body.Name, VlanId: body.VlanId, Enabled: body.Enabled}, nil
			},
		})
		resp := resource.CreateResponse{State: netSchemaForTest(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID {
			t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
		}
		if gotBody.Name != "iot" || gotBody.VlanId != 42 || !gotBody.Enabled || gotBody.Management != "UNMANAGED" {
			t.Errorf("body = %+v, want iot/42/enabled/UNMANAGED", gotBody)
		}
		var got networkModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.ID.ValueString() != newID.String() {
			t.Errorf("id = %q, want %q", got.ID.ValueString(), newID.String())
		}
		if got.Name.ValueString() != "iot" {
			t.Errorf("name = %q, want iot", got.Name.ValueString())
		}
	})
}

func TestNetworkRead(t *testing.T) {
	id := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	stateModel := networkModel{
		ID:      types.StringValue(id.String()),
		Name:    types.StringValue("iot"),
		VlanID:  types.Int64Value(42),
		Enabled: types.BoolValue(true),
	}

	t.Run("bad state surfaces diagnostics", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		shell := netSchemaForTest(t)
		resp := resource.ReadResponse{State: netSchemaForTest(t)}
		r.Read(context.Background(), resource.ReadRequest{State: shell}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null state")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		bad := stateModel
		bad.ID = types.StringValue("not-a-uuid")
		st := netStateOf(t, bad)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid network id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found removes resource", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.NetworkDetails, error) {
				return nil, fmt.Errorf("get network: %w", unifi.ErrNotFound)
			},
		})
		st := netStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("state not removed for missing network")
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.NetworkDetails, error) {
				return nil, errors.New("controller unreachable")
			},
		})
		st := netStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to read network") {
			t.Fatalf("expected read-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path refreshes state", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		r := netRes(t, &official.NetworksClientMock{
			GetFunc: func(_ context.Context, siteID, networkID uuid.UUID) (*official.NetworkDetails, error) {
				gotSite = siteID
				gotID = networkID
				return &official.NetworkDetails{Id: networkID, Name: "iot-renamed", VlanId: 43, Enabled: false}, nil
			},
		})
		st := netStateOf(t, stateModel)
		resp := resource.ReadResponse{State: st}
		r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID || gotID != id {
			t.Errorf("called with site %s id %s, want %s %s", gotSite, gotID, testutil.SiteID, id)
		}
		var got networkModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Name.ValueString() != "iot-renamed" || got.VlanID.ValueInt64() != 43 || got.Enabled.ValueBool() {
			t.Errorf("state = %+v, want iot-renamed/43/disabled", got)
		}
	})
}

func TestNetworkUpdate(t *testing.T) {
	id := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	planModel := networkModel{
		ID:      types.StringValue(id.String()),
		Name:    types.StringValue("iot"),
		VlanID:  types.Int64Value(44),
		Enabled: types.BoolValue(true),
	}

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{}, testutil.ReadOnly())
		resp := resource.UpdateResponse{State: netSchemaForTest(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan surfaces diagnostics", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		shell := netSchemaForTest(t)
		resp := resource.UpdateResponse{State: netSchemaForTest(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: tfsdk.Plan{Schema: shell.Schema, Raw: shell.Raw}}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null plan")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		bad := planModel
		bad.ID = types.StringValue("not-a-uuid")
		resp := resource.UpdateResponse{State: netSchemaForTest(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: netPlanOf(t, bad)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid network id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			UpdateFunc: func(context.Context, uuid.UUID, uuid.UUID, official.NetworkCreateOrUpdate) (*official.NetworkDetails, error) {
				return nil, errors.New("controller rejected the update")
			},
		})
		resp := resource.UpdateResponse{State: netSchemaForTest(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to update network") {
			t.Fatalf("expected update-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path sets state", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		var gotBody official.NetworkCreateOrUpdate
		r := netRes(t, &official.NetworksClientMock{
			UpdateFunc: func(_ context.Context, siteID, networkID uuid.UUID, body official.NetworkCreateOrUpdate) (*official.NetworkDetails, error) {
				gotSite = siteID
				gotID = networkID
				gotBody = body
				return &official.NetworkDetails{Id: networkID, Name: body.Name, VlanId: body.VlanId, Enabled: body.Enabled}, nil
			},
		})
		resp := resource.UpdateResponse{State: netSchemaForTest(t)}
		r.Update(context.Background(), resource.UpdateRequest{Plan: netPlanOf(t, planModel)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID || gotID != id {
			t.Errorf("called with site %s id %s, want %s %s", gotSite, gotID, testutil.SiteID, id)
		}
		if gotBody.VlanId != 44 {
			t.Errorf("vlanId = %d, want 44", gotBody.VlanId)
		}
		var got networkModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.VlanID.ValueInt64() != 44 {
			t.Errorf("vlan_id = %d, want 44", got.VlanID.ValueInt64())
		}
	})
}

func TestNetworkDelete(t *testing.T) {
	id := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	stateModel := networkModel{
		ID:      types.StringValue(id.String()),
		Name:    types.StringValue("iot"),
		VlanID:  types.Int64Value(42),
		Enabled: types.BoolValue(true),
	}

	t.Run("read-only guard blocks", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{}, testutil.ReadOnly())
		st := netStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("destroy protection blocks", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{}, testutil.DestroyProtection())
		st := netStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad state surfaces diagnostics", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		shell := netSchemaForTest(t)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: shell}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for null state")
		}
	})

	t.Run("invalid id errors", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{})
		bad := stateModel
		bad.ID = types.StringValue("not-a-uuid")
		st := netStateOf(t, bad)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Invalid network id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error surfaces", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID, *official.DeleteNetworkOptions) error {
				return errors.New("cannot delete the default network")
			},
		})
		st := netStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if !netDiagsContain(resp.Diagnostics, "Failed to delete network") {
			t.Fatalf("expected delete-failure diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found is success", func(t *testing.T) {
		r := netRes(t, &official.NetworksClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID, *official.DeleteNetworkOptions) error {
				return fmt.Errorf("delete network: %w", unifi.ErrNotFound)
			},
		})
		st := netStateOf(t, stateModel)
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics for already-deleted network: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path deletes", func(t *testing.T) {
		var gotSite, gotID uuid.UUID
		r := netRes(t, &official.NetworksClientMock{
			DeleteFunc: func(_ context.Context, siteID, networkID uuid.UUID, _ *official.DeleteNetworkOptions) error {
				gotSite = siteID
				gotID = networkID
				return nil
			},
		})
		st := netStateOf(t, stateModel)
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

func TestNetworkImportState(t *testing.T) {
	r := NewNetworkResource().(*networkResource)
	resp := resource.ImportStateResponse{State: netSchemaForTest(t)}
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
