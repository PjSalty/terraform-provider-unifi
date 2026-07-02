package resources

import (
	"context"
	"errors"
	"strings"
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
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// aclSchema returns the resource schema once per test, so plans and states can
// be built against the real attribute set.
func aclSchema(t *testing.T) tfsdk.Plan {
	t.Helper()
	var resp resource.SchemaResponse
	NewACLRuleResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return tfsdk.Plan{
		Schema: resp.Schema,
		Raw:    tftypes.NewValue(resp.Schema.Type().TerraformType(context.Background()), nil),
	}
}

// aclPlan builds a tfsdk.Plan carrying the given model.
func aclPlan(t *testing.T, m aclRuleModel) tfsdk.Plan {
	t.Helper()
	p := aclSchema(t)
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("plan set: %v", diags)
	}
	return p
}

// aclState builds a tfsdk.State carrying the given model.
func aclState(t *testing.T, m aclRuleModel) tfsdk.State {
	t.Helper()
	p := aclPlan(t, m)
	return tfsdk.State{Schema: p.Schema, Raw: p.Raw}
}

// aclBadRaw is a raw value that cannot decode into aclRuleModel, forcing the
// Plan.Get / State.Get diagnostics branches.
func aclBadRaw(t *testing.T) tftypes.Value {
	t.Helper()
	return tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{}}, map[string]tftypes.Value{})
}

// aclModel returns a minimal valid IPV4 rule model; tests override fields.
func aclModel(id string) aclRuleModel {
	var idVal types.String
	if id == "" {
		idVal = types.StringUnknown()
	} else {
		idVal = types.StringValue(id)
	}
	return aclRuleModel{
		ID:              idVal,
		Type:            types.StringValue("IPV4"),
		Name:            types.StringValue("unit-rule"),
		Action:          types.StringValue("ALLOW"),
		Enabled:         types.BoolValue(true),
		Description:     types.StringNull(),
		ProtocolFilter:  types.ListNull(types.StringType),
		NetworkIDFilter: types.StringNull(),
	}
}

// aclResource returns a configured resource whose client serves the mock.
func aclResource(t *testing.T, acls official.ACLsClient, opts ...testutil.Opt) *aclRuleResource {
	t.Helper()
	r := NewACLRuleResource().(*aclRuleResource)
	oc := &official.ClientMock{ACLsFunc: func() official.ACLsClient { return acls }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return r
}

// aclHasDiag reports whether any diagnostic summary or detail contains want.
func aclHasDiag(diags diag.Diagnostics, want string) bool {
	for _, d := range diags {
		if strings.Contains(d.Summary(), want) || strings.Contains(d.Detail(), want) {
			return true
		}
	}
	return false
}

func TestACLRuleMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	NewACLRuleResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_acl_rule" {
		t.Errorf("type name = %q, want unifi_acl_rule", resp.TypeName)
	}
}

func TestACLRuleSchemaAttributes(t *testing.T) {
	var resp resource.SchemaResponse
	NewACLRuleResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, name := range []string{"id", "type", "name", "action", "enabled", "source_filter", "destination_filter"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("schema missing %s attribute", name)
		}
	}
}

func TestACLRuleConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		r := NewACLRuleResource().(*aclRuleResource)
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
		r := NewACLRuleResource().(*aclRuleResource)
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Unexpected provider data") {
			t.Fatalf("expected wrong-type diagnostic, got: %v", resp.Diagnostics)
		}
		if r.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		if r.data == nil {
			t.Error("data not stored")
		}
	})
}

func TestACLRuleCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("read_only guard", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{}, testutil.ReadOnly())
		resp := resource.CreateResponse{State: aclState(t, aclModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: aclPlan(t, aclModel(""))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan errors", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		plan := aclSchema(t)
		plan.Raw = aclBadRaw(t)
		resp := resource.CreateResponse{State: aclState(t, aclModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable plan")
		}
	})

	t.Run("expand error stops before the API", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{}) // any API call would panic
		m := aclModel("")
		m.Type = types.StringValue("BOGUS")
		resp := resource.CreateResponse{State: aclState(t, aclModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: aclPlan(t, m)}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Unsupported ACL rule type") {
			t.Fatalf("expected unsupported-type diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			CreateRuleFunc: func(context.Context, uuid.UUID, official.ACLRuleUpdate) (*official.ACLRule, error) {
				return nil, errors.New("controller says no")
			},
		})
		resp := resource.CreateResponse{State: aclState(t, aclModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: aclPlan(t, aclModel(""))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Failed to create ACL rule") {
			t.Fatalf("expected create diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path stores the id", func(t *testing.T) {
		created := uuid.New()
		var gotSite uuid.UUID
		var gotBody official.ACLRuleUpdate
		r := aclResource(t, &official.ACLsClientMock{
			CreateRuleFunc: func(_ context.Context, site uuid.UUID, body official.ACLRuleUpdate) (*official.ACLRule, error) {
				gotSite = site
				gotBody = body
				return &official.ACLRule{Id: created}, nil
			},
		})
		resp := resource.CreateResponse{State: aclState(t, aclModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: aclPlan(t, aclModel(""))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID {
			t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
		}
		if gotBody.Name != "unit-rule" {
			t.Errorf("body name = %q, want unit-rule", gotBody.Name)
		}
		var got aclRuleModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.ID.ValueString() != created.String() {
			t.Errorf("id = %q, want %s", got.ID.ValueString(), created)
		}
	})
}

func TestACLRuleRead(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("bad state errors", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		state := aclState(t, aclModel(id.String()))
		state.Raw = aclBadRaw(t)
		resp := resource.ReadResponse{State: aclState(t, aclModel(id.String()))}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable state")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		state := aclState(t, aclModel("not-a-uuid"))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Invalid ACL rule id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found removes the resource", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			GetRuleFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.ACLRule, error) {
				return nil, unifi.ErrNotFound
			},
		})
		state := aclState(t, aclModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("state not removed on ErrNotFound")
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			GetRuleFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.ACLRule, error) {
				return nil, errors.New("boom")
			},
		})
		state := aclState(t, aclModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Failed to read ACL rule") {
			t.Fatalf("expected read diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path refreshes fields", func(t *testing.T) {
		desc := "refreshed"
		var gotID uuid.UUID
		r := aclResource(t, &official.ACLsClientMock{
			GetRuleFunc: func(_ context.Context, _ uuid.UUID, ruleID uuid.UUID) (*official.ACLRule, error) {
				gotID = ruleID
				return &official.ACLRule{
					Id:          id,
					Name:        "renamed",
					Action:      official.ACLRuleAction("BLOCK"),
					Enabled:     false,
					Type:        "IPV4",
					Description: &desc,
				}, nil
			},
		})
		state := aclState(t, aclModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("rule id = %s, want %s", gotID, id)
		}
		var got aclRuleModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Name.ValueString() != "renamed" {
			t.Errorf("name = %q, want renamed", got.Name.ValueString())
		}
		if got.Action.ValueString() != "BLOCK" {
			t.Errorf("action = %q, want BLOCK", got.Action.ValueString())
		}
		if got.Enabled.ValueBool() {
			t.Error("enabled = true, want false")
		}
		if got.Description.ValueString() != "refreshed" {
			t.Errorf("description = %q, want refreshed", got.Description.ValueString())
		}
	})

	t.Run("nil description reads as null", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			GetRuleFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.ACLRule, error) {
				return &official.ACLRule{Id: id, Name: "n", Action: "ALLOW", Enabled: true, Type: "MAC"}, nil
			},
		})
		m := aclModel(id.String())
		m.Description = types.StringValue("stale")
		state := aclState(t, m)
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		var got aclRuleModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if !got.Description.IsNull() {
			t.Errorf("description = %v, want null", got.Description)
		}
	})
}

func TestACLRuleUpdate(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("read_only guard", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{}, testutil.ReadOnly())
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: aclPlan(t, aclModel(id.String()))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan errors", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		plan := aclSchema(t)
		plan.Raw = aclBadRaw(t)
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable plan")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: aclPlan(t, aclModel("not-a-uuid"))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Invalid ACL rule id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("expand error", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		m := aclModel(id.String())
		m.Type = types.StringValue("BOGUS")
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: aclPlan(t, m)}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Unsupported ACL rule type") {
			t.Fatalf("expected unsupported-type diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			UpdateRuleFunc: func(context.Context, uuid.UUID, uuid.UUID, official.ACLRuleUpdate) (*official.ACLRule, error) {
				return nil, errors.New("boom")
			},
		})
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: aclPlan(t, aclModel(id.String()))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Failed to update ACL rule") {
			t.Fatalf("expected update diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path persists the plan", func(t *testing.T) {
		var gotID uuid.UUID
		var gotBody official.ACLRuleUpdate
		r := aclResource(t, &official.ACLsClientMock{
			UpdateRuleFunc: func(_ context.Context, _ uuid.UUID, ruleID uuid.UUID, body official.ACLRuleUpdate) (*official.ACLRule, error) {
				gotID = ruleID
				gotBody = body
				return &official.ACLRule{Id: ruleID}, nil
			},
		})
		m := aclModel(id.String())
		m.Name = types.StringValue("renamed")
		resp := resource.UpdateResponse{State: aclState(t, aclModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: aclPlan(t, m)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("rule id = %s, want %s", gotID, id)
		}
		if gotBody.Name != "renamed" {
			t.Errorf("body name = %q, want renamed", gotBody.Name)
		}
		var got aclRuleModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Name.ValueString() != "renamed" {
			t.Errorf("state name = %q, want renamed", got.Name.ValueString())
		}
	})
}

func TestACLRuleDelete(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("read_only guard", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{}, testutil.ReadOnly())
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel(id.String()))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("destroy_protection guard", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{}, testutil.DestroyProtection())
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel(id.String()))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad state errors", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		state := aclState(t, aclModel(id.String()))
		state.Raw = aclBadRaw(t)
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable state")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel("not-a-uuid"))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Invalid ACL rule id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			DeleteRuleFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
				return errors.New("boom")
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel(id.String()))}, &resp)
		if !aclHasDiag(resp.Diagnostics, "Failed to delete ACL rule") {
			t.Fatalf("expected delete diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found is success", func(t *testing.T) {
		r := aclResource(t, &official.ACLsClientMock{
			DeleteRuleFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
				return unifi.ErrNotFound
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel(id.String()))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		var gotID uuid.UUID
		r := aclResource(t, &official.ACLsClientMock{
			DeleteRuleFunc: func(_ context.Context, _ uuid.UUID, ruleID uuid.UUID) error {
				gotID = ruleID
				return nil
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: aclState(t, aclModel(id.String()))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("rule id = %s, want %s", gotID, id)
		}
	})
}

func TestACLRuleImportState(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	r := NewACLRuleResource().(*aclRuleResource)
	p := aclSchema(t)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: p.Schema, Raw: p.Raw}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: id.String()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}
	var got types.String
	if diags := resp.State.GetAttribute(ctx, path.Root("id"), &got); diags.HasError() {
		t.Fatalf("get id: %v", diags)
	}
	if got.ValueString() != id.String() {
		t.Errorf("imported id = %q, want %s", got.ValueString(), id)
	}
}

// TestExpandMACACLRuleInvalidNetworkID covers the network_id_filter parse
// failure inside the MAC variant.
func TestExpandMACACLRuleInvalidNetworkID(t *testing.T) {
	m := aclModel("")
	m.Type = types.StringValue("MAC")
	m.NetworkIDFilter = types.StringValue("not-a-uuid")
	_, diags := expandACLRule(context.Background(), m)
	if !aclHasDiag(diags, "Invalid network_id_filter") {
		t.Fatalf("expected invalid network_id_filter diagnostic, got: %v", diags)
	}
}

// TestExpandMACACLRuleDestinationFilter covers the MAC destination-filter
// assignment (the marshal-shape tests only exercise the source side).
func TestExpandMACACLRuleDestinationFilter(t *testing.T) {
	m := aclModel("")
	m.Type = types.StringValue("MAC")
	m.DestinationFilter = &aclRuleEndpointModel{
		IPAddressesOrSubnets: types.ListNull(types.StringType),
		Ports:                types.ListNull(types.Int64Type),
		MACAddresses: types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("aa:bb:cc:00:11:22"),
		}),
		PrefixLength: types.Int64Null(),
	}
	body, diags := expandACLRule(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	got := mustMarshalToMap(t, body)
	dst, ok := got["destinationFilter"].(map[string]any)
	if !ok {
		t.Fatalf("destinationFilter not an object: %v", got["destinationFilter"])
	}
	macs, ok := dst["macAddresses"].([]any)
	if !ok || len(macs) != 1 || macs[0] != "aa:bb:cc:00:11:22" {
		t.Errorf("destinationFilter.macAddresses = %v, want [aa:bb:cc:00:11:22]", dst["macAddresses"])
	}
	if _, present := dst["prefixLength"]; present {
		t.Errorf("prefixLength should be omitted when null, got %v", dst["prefixLength"])
	}
}

// TestExpandProtocolsInvalid covers the unsupported-protocol diagnostic.
func TestExpandProtocolsInvalid(t *testing.T) {
	var diags diag.Diagnostics
	l := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("ICMP")})
	out := expandProtocols(context.Background(), l, &diags)
	if !aclHasDiag(diags, "Invalid protocol") {
		t.Fatalf("expected invalid-protocol diagnostic, got: %v", diags)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, want empty", out)
	}
}

// TestExpandIPEndpointNilCases covers the early-nil returns.
func TestExpandIPEndpointNilCases(t *testing.T) {
	var diags diag.Diagnostics
	if got := expandIPEndpoint(context.Background(), nil, &diags); got != nil {
		t.Errorf("nil endpoint model: got %v, want nil", got)
	}
	empty := &aclRuleEndpointModel{
		IPAddressesOrSubnets: types.ListNull(types.StringType),
		Ports:                types.ListNull(types.Int64Type),
		MACAddresses:         types.ListNull(types.StringType),
		PrefixLength:         types.Int64Null(),
	}
	if got := expandIPEndpoint(context.Background(), empty, &diags); got != nil {
		t.Errorf("empty endpoint model: got %v, want nil", got)
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

// TestExpandMACEndpointNilCases covers the early-nil returns.
func TestExpandMACEndpointNilCases(t *testing.T) {
	var diags diag.Diagnostics
	if got := expandMACEndpoint(context.Background(), nil, &diags); got != nil {
		t.Errorf("nil endpoint model: got %v, want nil", got)
	}
	empty := &aclRuleEndpointModel{
		IPAddressesOrSubnets: types.ListNull(types.StringType),
		Ports:                types.ListNull(types.Int64Type),
		MACAddresses:         types.ListNull(types.StringType),
		PrefixLength:         types.Int64Null(),
	}
	if got := expandMACEndpoint(context.Background(), empty, &diags); got != nil {
		t.Errorf("empty endpoint model: got %v, want nil", got)
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

// TestListToStringsUnknown covers the unknown-list early return.
func TestListToStringsUnknown(t *testing.T) {
	var diags diag.Diagnostics
	if got := listToStrings(context.Background(), types.ListUnknown(types.StringType), &diags); got != nil {
		t.Errorf("unknown list: got %v, want nil", got)
	}
}
