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
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// --- harness -----------------------------------------------------------------

// fzSchema returns the resource schema (covering Schema itself on first use).
func fzSchema(t *testing.T) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	(&firewallZoneResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("schema: %v", sr.Diagnostics)
	}
	return tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
}

// fzResource returns a firewall zone resource configured against the mock.
func fzResource(t *testing.T, fw *official.FirewallClientMock, opts ...testutil.Opt) *firewallZoneResource {
	t.Helper()
	r, ok := NewFirewallZoneResource().(*firewallZoneResource)
	if !ok {
		t.Fatal("NewFirewallZoneResource returned the wrong type")
	}
	oc := &official.ClientMock{FirewallFunc: func() official.FirewallClient { return fw }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure: %v", resp.Diagnostics)
	}
	return r
}

// fzPlan builds a typed plan carrying the model.
func fzPlan(t *testing.T, m firewallZoneModel) tfsdk.Plan {
	t.Helper()
	empty := fzSchema(t)
	p := tfsdk.Plan(empty)
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building plan: %v", d)
	}
	return p
}

// fzConfig builds the configuration the framework hands Update alongside the plan.
func fzConfig(t *testing.T, m firewallZoneModel) tfsdk.Config {
	t.Helper()
	s := fzState(t, m)
	return tfsdk.Config(s)
}

// fzCurrent answers the membership read Update issues when network_ids is not
// configured.
func fzCurrent(networks ...uuid.UUID) func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
	return func(_ context.Context, _, id uuid.UUID) (*official.FirewallZone, error) {
		return &official.FirewallZone{Id: id, Name: "iot-zone", NetworkIds: networks}, nil
	}
}

// fzState builds a typed state carrying the model.
func fzState(t *testing.T, m firewallZoneModel) tfsdk.State {
	t.Helper()
	s := fzSchema(t)
	if d := s.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building state: %v", d)
	}
	return s
}

// fzWantErr asserts diags carry an error whose summary contains want.
func fzWantErr(t *testing.T, diags diag.Diagnostics, want string) {
	t.Helper()
	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic containing %q, got none", want)
	}
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), want) {
			return
		}
	}
	t.Fatalf("no error summary contains %q, got %v", want, diags)
}

func fzIDSet(ids ...uuid.UUID) types.Set {
	vals := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		vals = append(vals, types.StringValue(id.String()))
	}
	return types.SetValueMust(types.StringType, vals)
}

// --- Metadata / Schema / Configure --------------------------------------------

func TestFirewallZoneMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	(&firewallZoneResource{}).Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_firewall_zone" {
		t.Errorf("type name = %q, want unifi_firewall_zone", resp.TypeName)
	}
}

func TestFirewallZoneSchemaAttributes(t *testing.T) {
	s := fzSchema(t)
	for _, name := range []string{"id", "name", "network_ids"} {
		if _, ok := s.Schema.GetAttributes()[name]; !ok {
			t.Errorf("schema missing attribute %q", name)
		}
	}
}

func TestFirewallZoneConfigure(t *testing.T) {
	r := &firewallZoneResource{}

	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() || r.data != nil {
		t.Errorf("nil provider data should be a no-op, diags %v", resp.Diagnostics)
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 42}, &resp)
	fzWantErr(t, resp.Diagnostics, "Unexpected provider data")
	if r.data != nil {
		t.Error("data should stay nil on wrong-type provider data")
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(&official.ClientMock{})}, &resp)
	if resp.Diagnostics.HasError() || r.data == nil {
		t.Errorf("configure with provider data failed: %v", resp.Diagnostics)
	}
}

// --- Create --------------------------------------------------------------------

func TestFirewallZoneCreateReadOnly(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{}, testutil.ReadOnly())
	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &resp)
	fzWantErr(t, resp.Diagnostics, "read-only")
}

func TestFirewallZoneCreateBadPlan(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	empty := fzSchema(t)
	req := resource.CreateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "not-an-object")}}
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func TestFirewallZoneCreateExpandError(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringUnknown(),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("not-a-uuid")}),
	})
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	fzWantErr(t, resp.Diagnostics, "Invalid UUID")
}

func TestFirewallZoneCreateAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		CreateZoneFunc: func(_ context.Context, site uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			if site != testutil.SiteID {
				t.Errorf("site = %s, want %s", site, testutil.SiteID)
			}
			if body.Name != "iot-zone" {
				t.Errorf("body name = %q, want iot-zone", body.Name)
			}
			return nil, errors.New("controller exploded")
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringUnknown(),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	fzWantErr(t, resp.Diagnostics, "Failed to create firewall zone")
}

func TestFirewallZoneCreateFlattenError(t *testing.T) {
	dup := uuid.New()
	fw := &official.FirewallClientMock{
		CreateZoneFunc: func(context.Context, uuid.UUID, official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			// Duplicate network IDs are unrepresentable in a Terraform set, so
			// flattening them must surface a diagnostic.
			return &official.FirewallZone{Id: uuid.New(), Name: "iot-zone", NetworkIds: []uuid.UUID{dup, dup}}, nil
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringUnknown(),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected duplicate network IDs to fail flattening")
	}
}

func TestFirewallZoneCreateOK(t *testing.T) {
	zoneID, netID := uuid.New(), uuid.New()
	fw := &official.FirewallClientMock{
		CreateZoneFunc: func(_ context.Context, _ uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			return &official.FirewallZone{Id: zoneID, Name: body.Name, NetworkIds: body.NetworkIds}, nil
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringUnknown(),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: fzIDSet(netID),
	})
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	var got firewallZoneModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != zoneID.String() {
		t.Errorf("id = %q, want %q", got.ID.ValueString(), zoneID)
	}
	if got.Name.ValueString() != "iot-zone" {
		t.Errorf("name = %q, want iot-zone", got.Name.ValueString())
	}
	var ids []string
	if d := got.NetworkIDs.ElementsAs(context.Background(), &ids, false); d.HasError() {
		t.Fatalf("network ids: %v", d)
	}
	if len(ids) != 1 || ids[0] != netID.String() {
		t.Errorf("network_ids = %v, want [%s]", ids, netID)
	}
}

// --- Read ----------------------------------------------------------------------

func TestFirewallZoneReadBadState(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	empty := fzSchema(t)
	req := resource.ReadRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Bool, true)}}
	resp := resource.ReadResponse{State: fzSchema(t)}
	r.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestFirewallZoneReadBadID(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue("not-a-uuid"),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	fzWantErr(t, resp.Diagnostics, "Invalid firewall zone id")
}

func TestFirewallZoneReadNotFoundRemoves(t *testing.T) {
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
			return nil, fmt.Errorf("zone lookup: %w", unifi.ErrNotFound)
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found read should not error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state should be removed when the zone is gone")
	}
}

func TestFirewallZoneReadAPIError(t *testing.T) {
	writes := 0
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
			return nil, errors.New("boom")
		},
		UpdateZoneFunc: func(context.Context, uuid.UUID, uuid.UUID, official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			writes++
			return nil, errors.New("unexpected write")
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	fzWantErr(t, resp.Diagnostics, "Failed to read firewall zone")
}

func TestFirewallZoneReadFlattenError(t *testing.T) {
	dup := uuid.New()
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
			return &official.FirewallZone{Id: uuid.New(), Name: "iot-zone", NetworkIds: []uuid.UUID{dup, dup}}, nil
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected duplicate network IDs to fail flattening")
	}
}

func TestFirewallZoneReadOK(t *testing.T) {
	zoneID, netID := uuid.New(), uuid.New()
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) (*official.FirewallZone, error) {
			if site != testutil.SiteID || id != zoneID {
				t.Errorf("get zone called with site %s id %s", site, id)
			}
			return &official.FirewallZone{Id: zoneID, Name: "renamed-zone", NetworkIds: []uuid.UUID{netID}}, nil
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(zoneID.String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got firewallZoneModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.Name.ValueString() != "renamed-zone" {
		t.Errorf("name = %q, want renamed-zone (refreshed)", got.Name.ValueString())
	}
}

// --- Update --------------------------------------------------------------------

func TestFirewallZoneUpdateReadOnly(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{}, testutil.ReadOnly())
	var resp resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{}, &resp)
	fzWantErr(t, resp.Diagnostics, "read-only")
}

func TestFirewallZoneUpdateBadPlan(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	empty := fzSchema(t)
	req := resource.UpdateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Number, 7)}}
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func TestFirewallZoneUpdateBadID(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringValue("not-a-uuid"),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, Config: fzConfig(t, fzPlanModel(t, plan))}, &resp)
	fzWantErr(t, resp.Diagnostics, "Invalid firewall zone id")
}

func TestFirewallZoneUpdateExpandError(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("not-a-uuid")}),
	})
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, Config: fzConfig(t, fzPlanModel(t, plan))}, &resp)
	fzWantErr(t, resp.Diagnostics, "Invalid UUID")
}

func TestFirewallZoneUpdateAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		GetZoneFunc: fzCurrent(),
		UpdateZoneFunc: func(context.Context, uuid.UUID, uuid.UUID, official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			return nil, errors.New("boom")
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, Config: fzConfig(t, fzPlanModel(t, plan))}, &resp)
	fzWantErr(t, resp.Diagnostics, "Failed to update firewall zone")
}

func TestFirewallZoneUpdateFlattenError(t *testing.T) {
	dup := uuid.New()
	fw := &official.FirewallClientMock{
		GetZoneFunc: fzCurrent(),
		UpdateZoneFunc: func(context.Context, uuid.UUID, uuid.UUID, official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			return &official.FirewallZone{Id: uuid.New(), Name: "iot-zone", NetworkIds: []uuid.UUID{dup, dup}}, nil
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, Config: fzConfig(t, fzPlanModel(t, plan))}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected duplicate network IDs to fail flattening")
	}
}

func TestFirewallZoneUpdateOK(t *testing.T) {
	zoneID := uuid.New()
	fw := &official.FirewallClientMock{
		GetZoneFunc: fzCurrent(),
		UpdateZoneFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			if site != testutil.SiteID || id != zoneID {
				t.Errorf("update zone called with site %s id %s", site, id)
			}
			return &official.FirewallZone{Id: zoneID, Name: body.Name, NetworkIds: body.NetworkIds}, nil
		},
	}
	r := fzResource(t, fw)
	plan := fzPlan(t, firewallZoneModel{
		ID:         types.StringValue(zoneID.String()),
		Name:       types.StringValue("guest-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, Config: fzConfig(t, fzPlanModel(t, plan))}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	var got firewallZoneModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.Name.ValueString() != "guest-zone" {
		t.Errorf("name = %q, want guest-zone", got.Name.ValueString())
	}
}

// --- Delete --------------------------------------------------------------------

func TestFirewallZoneDeleteGuards(t *testing.T) {
	for name, opt := range map[string]testutil.Opt{
		"read_only":          testutil.ReadOnly(),
		"destroy_protection": testutil.DestroyProtection(),
	} {
		t.Run(name, func(t *testing.T) {
			r := fzResource(t, &official.FirewallClientMock{}, opt)
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{}, &resp)
			fzWantErr(t, resp.Diagnostics, "Delete blocked")
		})
	}
}

func TestFirewallZoneDeleteBadState(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	empty := fzSchema(t)
	req := resource.DeleteRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "junk")}}
	var resp resource.DeleteResponse
	r.Delete(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestFirewallZoneDeleteBadID(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue("not-a-uuid"),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	fzWantErr(t, resp.Diagnostics, "Invalid firewall zone id")
}

func TestFirewallZoneDeleteAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		DeleteZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New("zone is system-defined")
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	fzWantErr(t, resp.Diagnostics, "Failed to delete firewall zone")
}

func TestFirewallZoneDeleteNotFoundIsSuccess(t *testing.T) {
	fw := &official.FirewallClientMock{
		DeleteZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return fmt.Errorf("delete: %w", unifi.ErrNotFound)
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found delete should succeed: %v", resp.Diagnostics)
	}
}

func TestFirewallZoneDeleteOK(t *testing.T) {
	zoneID := uuid.New()
	fw := &official.FirewallClientMock{
		DeleteZoneFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) error {
			if site != testutil.SiteID || id != zoneID {
				t.Errorf("delete zone called with site %s id %s", site, id)
			}
			return nil
		},
	}
	r := fzResource(t, fw)
	st := fzState(t, firewallZoneModel{
		ID:         types.StringValue(zoneID.String()),
		Name:       types.StringValue("iot-zone"),
		NetworkIDs: types.SetNull(types.StringType),
	})
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
}

// --- ImportState -----------------------------------------------------------------

func TestFirewallZoneImportState(t *testing.T) {
	r := fzResource(t, &official.FirewallClientMock{})
	id := uuid.New().String()
	resp := resource.ImportStateResponse{State: fzSchema(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	var got firewallZoneModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != id {
		t.Errorf("imported id = %q, want %q", got.ID.ValueString(), id)
	}
}

// TestFlattenFirewallZoneDirect exercises the flatten helper without CRUD noise.
func TestFlattenFirewallZoneDirect(t *testing.T) {
	zoneID, netID := uuid.New(), uuid.New()
	m, diags := flattenFirewallZone(context.Background(),
		&official.FirewallZone{Id: zoneID, Name: "iot-zone", NetworkIds: []uuid.UUID{netID}})
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if m.ID.ValueString() != zoneID.String() || m.Name.ValueString() != "iot-zone" {
		t.Errorf("flatten = %+v", m)
	}
	var ids []string
	if d := m.NetworkIDs.ElementsAs(context.Background(), &ids, false); d.HasError() {
		t.Fatalf("elements: %v", d)
	}
	if len(ids) != 1 || ids[0] != netID.String() {
		t.Errorf("network ids = %v, want [%s]", ids, netID)
	}
}

// fzPlanModel reads a plan back into the model so Config can mirror it.
func fzPlanModel(t *testing.T, p tfsdk.Plan) firewallZoneModel {
	t.Helper()
	var m firewallZoneModel
	if d := p.Get(context.Background(), &m); d.HasError() {
		t.Fatalf("reading plan: %v", d)
	}
	return m
}

// Omitting network_ids declares a zone whose membership another resource owns
// (unifi_network's gateway.zone_id). The API needs networkIds on every write,
// so Update writes back the controller's current membership; sending the
// unknown planned value as [] would empty the zone on a rename.
func TestFirewallZoneUpdateOmittedNetworkIDsPreservesMembership(t *testing.T) {
	zoneID, a, b := uuid.New(), uuid.New(), uuid.New()
	var sent []uuid.UUID
	fw := &official.FirewallClientMock{
		GetZoneFunc: fzCurrent(a, b),
		UpdateZoneFunc: func(_ context.Context, _, _ uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			sent = body.NetworkIds
			return &official.FirewallZone{Id: zoneID, Name: body.Name, NetworkIds: body.NetworkIds}, nil
		},
	}
	r := fzResource(t, fw)
	m := firewallZoneModel{
		ID:         types.StringValue(zoneID.String()),
		Name:       types.StringValue("iot-renamed"),
		NetworkIDs: types.SetUnknown(types.StringType),
	}
	cfg := m
	cfg.NetworkIDs = types.SetNull(types.StringType)
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fzPlan(t, m), Config: fzConfig(t, cfg)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	if len(sent) != 2 {
		t.Fatalf("update sent networkIds %v; an unconfigured zone must keep its controller membership %v", sent, []uuid.UUID{a, b})
	}
	var got firewallZoneModel
	resp.State.Get(context.Background(), &got)
	if got.NetworkIDs.IsUnknown() || got.NetworkIDs.IsNull() || len(got.NetworkIDs.Elements()) != 2 {
		t.Errorf("state network_ids = %s, want the two networks the controller reported", got.NetworkIDs)
	}
}

// A configured list is authoritative, so Update must send it verbatim and must
// not consult the controller's membership.
func TestFirewallZoneUpdateConfiguredNetworkIDsIsAuthoritative(t *testing.T) {
	zoneID, want := uuid.New(), uuid.New()
	reads := 0
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
			reads++
			return nil, errors.New("unexpected membership read")
		},
		UpdateZoneFunc: func(_ context.Context, _, _ uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			if len(body.NetworkIds) != 1 || body.NetworkIds[0] != want {
				t.Errorf("update sent networkIds %v, want exactly [%s]", body.NetworkIds, want)
			}
			return &official.FirewallZone{Id: zoneID, Name: body.Name, NetworkIds: body.NetworkIds}, nil
		},
	}
	r := fzResource(t, fw)
	m := firewallZoneModel{
		ID:         types.StringValue(zoneID.String()),
		Name:       types.StringValue("iot"),
		NetworkIDs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue(want.String())}),
	}
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fzPlan(t, m), Config: fzConfig(t, m)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	if reads != 0 {
		t.Errorf("GetZone called %d times; a configured network_ids list must not read controller membership", reads)
	}
}

func TestFirewallZoneUpdateMembershipReadError(t *testing.T) {
	writes := 0
	fw := &official.FirewallClientMock{
		GetZoneFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallZone, error) {
			return nil, errors.New("boom")
		},
		UpdateZoneFunc: func(context.Context, uuid.UUID, uuid.UUID, official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			writes++
			return nil, errors.New("unexpected write")
		},
	}
	r := fzResource(t, fw)
	m := firewallZoneModel{
		ID:         types.StringValue(uuid.New().String()),
		Name:       types.StringValue("iot"),
		NetworkIDs: types.SetNull(types.StringType),
	}
	resp := resource.UpdateResponse{State: fzSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fzPlan(t, m), Config: fzConfig(t, m)}, &resp)
	fzWantErr(t, resp.Diagnostics, "Failed to read firewall zone membership")
	if writes != 0 {
		t.Errorf("UpdateZone called %d times after the membership read failed; it must not write a guessed list", writes)
	}
}

// Issue #11. With network_ids omitted, Create is handed an unknown value for
// the Optional+Computed attribute and may fill it in; before the fix it was a
// planned null, and writing the controller's [] back failed the apply.
func TestFirewallZoneCreateOmittedNetworkIDsIsConsistent(t *testing.T) {
	zoneID := uuid.New()
	r := fzResource(t, &official.FirewallClientMock{
		CreateZoneFunc: func(_ context.Context, _ uuid.UUID, body official.FirewallZoneCreateOrUpdate) (*official.FirewallZone, error) {
			return &official.FirewallZone{Id: zoneID, Name: body.Name, NetworkIds: body.NetworkIds}, nil
		},
	})
	attrSchema := fzSchema(t).Schema.GetAttributes()["network_ids"]
	if !attrSchema.IsComputed() {
		t.Fatal("network_ids must be Computed: an omitted value plans null otherwise, and Create's known [] contradicts it")
	}
	resp := resource.CreateResponse{State: fzSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: fzPlan(t, firewallZoneModel{
		ID:         types.StringUnknown(),
		Name:       types.StringValue("IoT"),
		NetworkIDs: types.SetUnknown(types.StringType),
	})}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	var got firewallZoneModel
	resp.State.Get(context.Background(), &got)
	if got.NetworkIDs.IsUnknown() || got.NetworkIDs.IsNull() {
		t.Errorf("state network_ids = %s; Create must resolve the unknown to the controller's known set", got.NetworkIDs)
	}
}
