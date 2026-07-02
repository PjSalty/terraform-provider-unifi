package resources

import (
	"context"
	"errors"
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
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// dnspSchema returns an empty plan built against the real resource schema.
func dnspSchema(t *testing.T) tfsdk.Plan {
	t.Helper()
	var resp resource.SchemaResponse
	NewDNSPolicyResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return tfsdk.Plan{
		Schema: resp.Schema,
		Raw:    tftypes.NewValue(resp.Schema.Type().TerraformType(context.Background()), nil),
	}
}

// dnspPlan builds a tfsdk.Plan carrying the given model.
func dnspPlan(t *testing.T, m dnsPolicyModel) tfsdk.Plan {
	t.Helper()
	p := dnspSchema(t)
	if diags := p.Set(context.Background(), &m); diags.HasError() {
		t.Fatalf("plan set: %v", diags)
	}
	return p
}

// dnspState builds a tfsdk.State carrying the given model.
func dnspState(t *testing.T, m dnsPolicyModel) tfsdk.State {
	t.Helper()
	p := dnspPlan(t, m)
	return tfsdk.State{Schema: p.Schema, Raw: p.Raw}
}

// dnspBadRaw is a raw value that cannot decode into dnsPolicyModel.
func dnspBadRaw(t *testing.T) tftypes.Value {
	t.Helper()
	return tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{}}, map[string]tftypes.Value{})
}

// dnspModel returns a minimal valid A_RECORD model; tests override fields.
func dnspModel(id string) dnsPolicyModel {
	var idVal types.String
	if id == "" {
		idVal = types.StringUnknown()
	} else {
		idVal = types.StringValue(id)
	}
	return dnsPolicyModel{
		ID:          idVal,
		Type:        types.StringValue("A_RECORD"),
		Enabled:     types.BoolValue(true),
		Domain:      types.StringValue("host.example.com"),
		IPv4Address: types.StringValue("192.0.2.5"),
	}
}

// dnspResource returns a configured resource whose client serves the mock.
func dnspResource(t *testing.T, dns official.DNSPoliciesClient, opts ...testutil.Opt) *dnsPolicyResource {
	t.Helper()
	r := NewDNSPolicyResource().(*dnsPolicyResource)
	oc := &official.ClientMock{DNSPoliciesFunc: func() official.DNSPoliciesClient { return dns }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return r
}

// dnspHasDiag reports whether any diagnostic summary or detail contains want.
func dnspHasDiag(diags diag.Diagnostics, want string) bool {
	for _, d := range diags {
		if strings.Contains(d.Summary(), want) || strings.Contains(d.Detail(), want) {
			return true
		}
	}
	return false
}

func TestDNSPolicyMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	NewDNSPolicyResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_dns_policy" {
		t.Errorf("type name = %q, want unifi_dns_policy", resp.TypeName)
	}
}

func TestDNSPolicySchemaAttributes(t *testing.T) {
	var resp resource.SchemaResponse
	NewDNSPolicyResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, name := range []string{"id", "type", "enabled", "domain", "ipv4_address", "text", "ip_address"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("schema missing %s attribute", name)
		}
	}
}

func TestDNSPolicyConfigure(t *testing.T) {
	t.Run("nil provider data is a no-op", func(t *testing.T) {
		r := NewDNSPolicyResource().(*dnsPolicyResource)
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
		r := NewDNSPolicyResource().(*dnsPolicyResource)
		var resp resource.ConfigureResponse
		r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "nope"}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Unexpected provider data") {
			t.Fatalf("expected wrong-type diagnostic, got: %v", resp.Diagnostics)
		}
		if r.data != nil {
			t.Error("data set despite wrong provider data type")
		}
	})
	t.Run("valid data stored", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		if r.data == nil {
			t.Error("data not stored")
		}
	})
}

func TestDNSPolicyCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("read_only guard", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{}, testutil.ReadOnly())
		resp := resource.CreateResponse{State: dnspState(t, dnspModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: dnspPlan(t, dnspModel(""))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan errors", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		plan := dnspSchema(t)
		plan.Raw = dnspBadRaw(t)
		resp := resource.CreateResponse{State: dnspState(t, dnspModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable plan")
		}
	})

	t.Run("expand error stops before the API", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{}) // any API call would panic
		m := dnspModel("")
		m.Type = types.StringValue("BOGUS")
		resp := resource.CreateResponse{State: dnspState(t, dnspModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: dnspPlan(t, m)}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Unsupported DNS policy type") {
			t.Fatalf("expected unsupported-type diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			CreateFunc: func(context.Context, uuid.UUID, official.DNSPolicyCreateOrUpdate) (*official.DNSPolicy, error) {
				return nil, errors.New("controller says no")
			},
		})
		resp := resource.CreateResponse{State: dnspState(t, dnspModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: dnspPlan(t, dnspModel(""))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Failed to create DNS policy") {
			t.Fatalf("expected create diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path stores the id", func(t *testing.T) {
		created := uuid.New()
		var gotSite uuid.UUID
		var gotBody official.DNSPolicyCreateOrUpdate
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			CreateFunc: func(_ context.Context, site uuid.UUID, body official.DNSPolicyCreateOrUpdate) (*official.DNSPolicy, error) {
				gotSite = site
				gotBody = body
				return &official.DNSPolicy{Id: created}, nil
			},
		})
		resp := resource.CreateResponse{State: dnspState(t, dnspModel(""))}
		r.Create(ctx, resource.CreateRequest{Plan: dnspPlan(t, dnspModel(""))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}
		if gotSite != testutil.SiteID {
			t.Errorf("site = %s, want %s", gotSite, testutil.SiteID)
		}
		if gotBody.Type != "A_RECORD" {
			t.Errorf("body type = %q, want A_RECORD", gotBody.Type)
		}
		var got dnsPolicyModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.ID.ValueString() != created.String() {
			t.Errorf("id = %q, want %s", got.ID.ValueString(), created)
		}
	})
}

func TestDNSPolicyRead(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("bad state errors", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		state := dnspState(t, dnspModel(id.String()))
		state.Raw = dnspBadRaw(t)
		resp := resource.ReadResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable state")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		state := dnspState(t, dnspModel("not-a-uuid"))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Invalid DNS policy id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found removes the resource", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.DNSPolicy, error) {
				return nil, unifi.ErrNotFound
			},
		})
		state := dnspState(t, dnspModel(id.String()))
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
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.DNSPolicy, error) {
				return nil, errors.New("boom")
			},
		})
		state := dnspState(t, dnspModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Failed to read DNS policy") {
			t.Fatalf("expected read diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path refreshes fields", func(t *testing.T) {
		dom := "moved.example.com"
		var gotID uuid.UUID
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			GetFunc: func(_ context.Context, _ uuid.UUID, policyID uuid.UUID) (*official.DNSPolicy, error) {
				gotID = policyID
				return &official.DNSPolicy{Id: id, Type: "A_RECORD", Enabled: false, Domain: &dom}, nil
			},
		})
		state := dnspState(t, dnspModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("policy id = %s, want %s", gotID, id)
		}
		var got dnsPolicyModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Domain.ValueString() != "moved.example.com" {
			t.Errorf("domain = %q, want moved.example.com", got.Domain.ValueString())
		}
		if got.Enabled.ValueBool() {
			t.Error("enabled = true, want false")
		}
	})

	t.Run("nil domain keeps prior state", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			GetFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.DNSPolicy, error) {
				return &official.DNSPolicy{Id: id, Type: "A_RECORD", Enabled: true}, nil
			},
		})
		state := dnspState(t, dnspModel(id.String()))
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", resp.Diagnostics)
		}
		var got dnsPolicyModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Domain.ValueString() != "host.example.com" {
			t.Errorf("domain = %q, want host.example.com (unchanged)", got.Domain.ValueString())
		}
	})
}

func TestDNSPolicyUpdate(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("read_only guard", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{}, testutil.ReadOnly())
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: dnspPlan(t, dnspModel(id.String()))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "read-only") {
			t.Fatalf("expected read-only diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad plan errors", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		plan := dnspSchema(t)
		plan.Raw = dnspBadRaw(t)
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable plan")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: dnspPlan(t, dnspModel("not-a-uuid"))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Invalid DNS policy id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("expand error", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		m := dnspModel(id.String())
		m.Type = types.StringValue("BOGUS")
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: dnspPlan(t, m)}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Unsupported DNS policy type") {
			t.Fatalf("expected unsupported-type diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			UpdateFunc: func(context.Context, uuid.UUID, uuid.UUID, official.DNSPolicyCreateOrUpdate) (*official.DNSPolicy, error) {
				return nil, errors.New("boom")
			},
		})
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: dnspPlan(t, dnspModel(id.String()))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Failed to update DNS policy") {
			t.Fatalf("expected update diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path persists the plan", func(t *testing.T) {
		var gotID uuid.UUID
		var gotBody official.DNSPolicyCreateOrUpdate
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			UpdateFunc: func(_ context.Context, _ uuid.UUID, policyID uuid.UUID, body official.DNSPolicyCreateOrUpdate) (*official.DNSPolicy, error) {
				gotID = policyID
				gotBody = body
				return &official.DNSPolicy{Id: policyID}, nil
			},
		})
		m := dnspModel(id.String())
		m.Domain = types.StringValue("renamed.example.com")
		resp := resource.UpdateResponse{State: dnspState(t, dnspModel(id.String()))}
		r.Update(ctx, resource.UpdateRequest{Plan: dnspPlan(t, m)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("policy id = %s, want %s", gotID, id)
		}
		if gotBody.Type != "A_RECORD" {
			t.Errorf("body type = %q, want A_RECORD", gotBody.Type)
		}
		var got dnsPolicyModel
		if diags := resp.State.Get(ctx, &got); diags.HasError() {
			t.Fatalf("state get: %v", diags)
		}
		if got.Domain.ValueString() != "renamed.example.com" {
			t.Errorf("state domain = %q, want renamed.example.com", got.Domain.ValueString())
		}
	})
}

func TestDNSPolicyDelete(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("read_only guard", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{}, testutil.ReadOnly())
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel(id.String()))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("destroy_protection guard", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{}, testutil.DestroyProtection())
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel(id.String()))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Delete blocked") {
			t.Fatalf("expected delete-blocked diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("bad state errors", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		state := dnspState(t, dnspModel(id.String()))
		state.Raw = dnspBadRaw(t)
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected diagnostics for undecodable state")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel("not-a-uuid"))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Invalid DNS policy id") {
			t.Fatalf("expected invalid-id diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("api error", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
				return errors.New("boom")
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel(id.String()))}, &resp)
		if !dnspHasDiag(resp.Diagnostics, "Failed to delete DNS policy") {
			t.Fatalf("expected delete diagnostic, got: %v", resp.Diagnostics)
		}
	})

	t.Run("not found is success", func(t *testing.T) {
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			DeleteFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
				return unifi.ErrNotFound
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel(id.String()))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		var gotID uuid.UUID
		r := dnspResource(t, &official.DNSPoliciesClientMock{
			DeleteFunc: func(_ context.Context, _ uuid.UUID, policyID uuid.UUID) error {
				gotID = policyID
				return nil
			},
		})
		var resp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: dnspState(t, dnspModel(id.String()))}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if gotID != id {
			t.Errorf("policy id = %s, want %s", gotID, id)
		}
	})
}

func TestDNSPolicyImportState(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	r := NewDNSPolicyResource().(*dnsPolicyResource)
	p := dnspSchema(t)
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

// TestExpandDNSPolicyRemainingVariants covers the AAAA, CNAME, MX, and TXT
// dispatch arms not exercised by the marshal-shape tests.
func TestExpandDNSPolicyRemainingVariants(t *testing.T) {
	cases := []struct {
		name  string
		model dnsPolicyModel
		field string
		want  any
	}{
		{
			name: "AAAA_RECORD",
			model: dnsPolicyModel{
				Type:        types.StringValue("AAAA_RECORD"),
				Enabled:     types.BoolValue(true),
				Domain:      types.StringValue("v6.example.com"),
				IPv6Address: types.StringValue("2001:db8::1"),
				TTLSeconds:  types.Int64Value(300),
			},
			field: "ipv6Address",
			want:  "2001:db8::1",
		},
		{
			name: "CNAME_RECORD",
			model: dnsPolicyModel{
				Type:         types.StringValue("CNAME_RECORD"),
				Enabled:      types.BoolValue(true),
				Domain:       types.StringValue("alias.example.com"),
				TargetDomain: types.StringValue("canonical.example.com"),
				TTLSeconds:   types.Int64Value(600),
			},
			field: "targetDomain",
			want:  "canonical.example.com",
		},
		{
			name: "MX_RECORD",
			model: dnsPolicyModel{
				Type:             types.StringValue("MX_RECORD"),
				Enabled:          types.BoolValue(true),
				Domain:           types.StringValue("example.com"),
				MailServerDomain: types.StringValue("mail.example.com"),
				Priority:         types.Int64Value(10),
			},
			field: "mailServerDomain",
			want:  "mail.example.com",
		},
		{
			name: "TXT_RECORD",
			model: dnsPolicyModel{
				Type:    types.StringValue("TXT_RECORD"),
				Enabled: types.BoolValue(true),
				Domain:  types.StringValue("example.com"),
				Text:    types.StringValue("v=spf1 -all"),
			},
			field: "text",
			want:  "v=spf1 -all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalDNSPolicy(t, tc.model)
			if got["type"] != tc.name {
				t.Errorf("type = %v, want %s", got["type"], tc.name)
			}
			if got[tc.field] != tc.want {
				t.Errorf("%s = %v, want %v", tc.field, got[tc.field], tc.want)
			}
		})
	}
}

// TestStrPtr covers every early-nil branch plus the value path.
func TestStrPtr(t *testing.T) {
	if strPtr(types.StringNull()) != nil {
		t.Error("null: want nil")
	}
	if strPtr(types.StringUnknown()) != nil {
		t.Error("unknown: want nil")
	}
	if strPtr(types.StringValue("")) != nil {
		t.Error("empty: want nil")
	}
	if got := strPtr(types.StringValue("x")); got == nil || *got != "x" {
		t.Errorf("value: got %v, want x", got)
	}
}

// TestInt32Ptr covers the nil and value paths.
func TestInt32Ptr(t *testing.T) {
	if int32Ptr(types.Int64Null()) != nil {
		t.Error("null: want nil")
	}
	if int32Ptr(types.Int64Unknown()) != nil {
		t.Error("unknown: want nil")
	}
	if got := int32Ptr(types.Int64Value(42)); got == nil || *got != 42 {
		t.Errorf("value: got %v, want 42", got)
	}
}
