package resources

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

var (
	_ resource.Resource                = &aclRuleResource{}
	_ resource.ResourceWithConfigure   = &aclRuleResource{}
	_ resource.ResourceWithImportState = &aclRuleResource{}
)

// NewACLRuleResource returns the unifi_acl_rule resource (a network/traffic ACL rule).
func NewACLRuleResource() resource.Resource {
	return &aclRuleResource{}
}

type aclRuleResource struct {
	data *providerdata.Data
}

// aclRuleEndpointModel is one side (source or destination) of a rule. For an
// IPV4 rule it carries IP addresses/subnets and an optional port list; for a MAC
// rule it carries MAC addresses and an optional prefix length.
type aclRuleEndpointModel struct {
	IPAddressesOrSubnets types.List  `tfsdk:"ip_addresses_or_subnets"`
	Ports                types.List  `tfsdk:"ports"`
	MACAddresses         types.List  `tfsdk:"mac_addresses"`
	PrefixLength         types.Int64 `tfsdk:"prefix_length"`
}

type aclRuleModel struct {
	ID                types.String          `tfsdk:"id"`
	Type              types.String          `tfsdk:"type"`
	Name              types.String          `tfsdk:"name"`
	Action            types.String          `tfsdk:"action"`
	Enabled           types.Bool            `tfsdk:"enabled"`
	Description       types.String          `tfsdk:"description"`
	ProtocolFilter    types.List            `tfsdk:"protocol_filter"`
	NetworkIDFilter   types.String          `tfsdk:"network_id_filter"`
	SourceFilter      *aclRuleEndpointModel `tfsdk:"source_filter"`
	DestinationFilter *aclRuleEndpointModel `tfsdk:"destination_filter"`
}

func (r *aclRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_acl_rule"
}

func (r *aclRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	endpointAttrs := map[string]schema.Attribute{
		"ip_addresses_or_subnets": schema.ListAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: "IP addresses or CIDR subnets this side matches. IPV4 rules only.",
		},
		"ports": schema.ListAttribute{
			Optional:    true,
			ElementType: types.Int64Type,
			Description: "Ports this side matches. Omit to match all ports. IPV4 rules only.",
		},
		"mac_addresses": schema.ListAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: "MAC addresses this side matches. MAC rules only.",
		},
		"prefix_length": schema.Int64Attribute{
			Optional:    true,
			Description: "MAC address prefix length (1-48). Omit to match full addresses. MAC rules only.",
			Validators:  []validator.Int64{int64validator.Between(1, 48)},
		},
	}

	resp.Schema = schema.Schema{
		Description: "A network traffic ACL rule on the UniFi controller. Either an IPV4 rule " +
			"(matching IP addresses/subnets, ports, and protocols) or a MAC rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "ACL rule UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "Rule type: IPV4 or MAC.",
				Validators:  []validator.String{stringvalidator.OneOf("IPV4", "MAC")},
				// Switching rule type rebuilds the rule; the union body differs entirely.
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "ACL rule name.",
			},
			"action": schema.StringAttribute{
				Required:    true,
				Description: "Rule action: ALLOW or BLOCK.",
				Validators:  []validator.String{stringvalidator.OneOf("ALLOW", "BLOCK")},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the rule is enabled.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Free-form rule description.",
			},
			"protocol_filter": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Protocols the rule applies to: any of TCP, UDP. Omit to match all. IPV4 rules only.",
			},
			"network_id_filter": schema.StringAttribute{
				Optional:    true,
				Description: "UUID of the network the rule applies to. MAC rules only.",
			},
			"source_filter": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Traffic source matcher.",
				Attributes:  endpointAttrs,
			},
			"destination_filter": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Traffic destination matcher.",
				Attributes:  endpointAttrs,
			},
		},
	}
}

func (r *aclRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d, ok := req.ProviderData.(*providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *providerdata.Data, got %T. This is a provider bug.", req.ProviderData))
		return
	}
	r.data = d
}

func (r *aclRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan aclRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandACLRule(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().ACLs().CreateRule(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create ACL rule", err.Error())
		return
	}
	plan.ID = types.StringValue(got.Id.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *aclRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state aclRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid ACL rule id", err.Error())
		return
	}
	got, err := r.data.Client.Official().ACLs().GetRule(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read ACL rule", err.Error())
		return
	}
	// Refresh the reliably-typed top-level fields. shortcut: the nested-union
	// filter sub-objects (source_filter, destination_filter, protocol_filter)
	// stay from prior state; on the read path they arrive as interface{} on the
	// ACLRule struct, so a typed refresh is added with the acceptance tests
	// (live controller) where the As* navigation can be verified.
	state.Name = types.StringValue(got.Name)
	state.Action = types.StringValue(string(got.Action))
	state.Enabled = types.BoolValue(got.Enabled)
	state.Type = types.StringValue(got.Type)
	if got.Description != nil {
		state.Description = types.StringValue(*got.Description)
	} else {
		state.Description = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *aclRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan aclRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid ACL rule id", err.Error())
		return
	}
	body, diags := expandACLRule(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.data.Client.Official().ACLs().UpdateRule(ctx, r.data.SiteID, id, body); err != nil {
		resp.Diagnostics.AddError("Failed to update ACL rule", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *aclRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state aclRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid ACL rule id", err.Error())
		return
	}
	if err := r.data.Client.Official().ACLs().DeleteRule(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete ACL rule", err.Error())
	}
}

func (r *aclRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandACLRule builds the create/update body for an ACL rule.
//
// In ACLRuleUpdate.MarshalJSON the named fields (action, name, enabled,
// description, type, sourceFilter, destinationFilter) are written AFTER (and so
// override) the union. sourceFilter/destinationFilter are typed interface{} on
// the struct, so we set them directly with the *IPACLRuleEndpoint /
// *MACACLRuleEndpoint values; relying on From*CreateUpdate alone would leave
// those named fields nil and the marshaler would emit them as null. The union
// (From*CreateUpdate) is still set so the discriminator and the variant-only
// fields (protocolFilter for IPV4, networkIdFilter for MAC) are carried.
func expandACLRule(ctx context.Context, m aclRuleModel) (official.ACLRuleUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics

	body := official.ACLRuleUpdate{
		Name:    m.Name.ValueString(),
		Action:  official.ACLRuleAction(m.Action.ValueString()),
		Enabled: m.Enabled.ValueBool(),
		Type:    m.Type.ValueString(),
	}
	if !m.Description.IsNull() && m.Description.ValueString() != "" {
		desc := m.Description.ValueString()
		body.Description = &desc
	}

	switch m.Type.ValueString() {
	case "IPV4":
		expandIPACLRule(ctx, m, &body, &diags)
	case "MAC":
		expandMACACLRule(ctx, m, &body, &diags)
	default:
		diags.AddError("Unsupported ACL rule type", m.Type.ValueString())
	}

	return body, diags
}

func expandIPACLRule(ctx context.Context, m aclRuleModel, body *official.ACLRuleUpdate, diags *diag.Diagnostics) {
	// Named interface{} fields override the union, so set them directly.
	if ep := expandIPEndpoint(ctx, m.SourceFilter, diags); ep != nil {
		body.SourceFilter = ep
	}
	if ep := expandIPEndpoint(ctx, m.DestinationFilter, diags); ep != nil {
		body.DestinationFilter = ep
	}

	// The variant carries protocolFilter and sets the IPV4 discriminator.
	variant := official.IpAclRuleCreateUpdate{}
	if protos := expandProtocols(ctx, m.ProtocolFilter, diags); protos != nil {
		variant.ProtocolFilter = &protos
	}
	if err := body.FromIpAclRuleCreateUpdate(variant); err != nil {
		diags.AddError("Failed to encode IPV4 ACL rule", err.Error())
	}
}

func expandMACACLRule(ctx context.Context, m aclRuleModel, body *official.ACLRuleUpdate, diags *diag.Diagnostics) {
	if ep := expandMACEndpoint(ctx, m.SourceFilter, diags); ep != nil {
		body.SourceFilter = ep
	}
	if ep := expandMACEndpoint(ctx, m.DestinationFilter, diags); ep != nil {
		body.DestinationFilter = ep
	}

	variant := official.MacAclRuleCreateUpdate{}
	if !m.NetworkIDFilter.IsNull() && m.NetworkIDFilter.ValueString() != "" {
		nid, err := uuid.Parse(m.NetworkIDFilter.ValueString())
		if err != nil {
			diags.AddError("Invalid network_id_filter", err.Error())
		} else {
			variant.NetworkIdFilter = &nid
		}
	}
	if err := body.FromMacAclRuleCreateUpdate(variant); err != nil {
		diags.AddError("Failed to encode MAC ACL rule", err.Error())
	}
}

func expandIPEndpoint(ctx context.Context, ep *aclRuleEndpointModel, diags *diag.Diagnostics) *official.IPACLRuleEndpoint {
	if ep == nil {
		return nil
	}
	subnets := listToStrings(ctx, ep.IPAddressesOrSubnets, diags)
	ports := listToInt32s(ctx, ep.Ports, diags)
	if subnets == nil && ports == nil {
		return nil
	}
	filter := official.IpAclRuleSubnetEndpointFilter{}
	if subnets != nil {
		filter.IpAddressesOrSubnets = &subnets
	}
	if ports != nil {
		filter.PortFilter = &ports
	}
	out := &official.IPACLRuleEndpoint{}
	if err := out.FromIpAclRuleSubnetEndpointFilter(filter); err != nil {
		diags.AddError("Failed to encode IP endpoint filter", err.Error())
		return nil
	}
	return out
}

func expandMACEndpoint(ctx context.Context, ep *aclRuleEndpointModel, diags *diag.Diagnostics) *official.MACACLRuleEndpoint {
	if ep == nil {
		return nil
	}
	macs := listToStrings(ctx, ep.MACAddresses, diags)
	if macs == nil {
		return nil
	}
	filter := official.MacAclRuleMacAddressEndpointFilter{MacAddresses: &macs}
	if !ep.PrefixLength.IsNull() && !ep.PrefixLength.IsUnknown() {
		pl := safeInt32(ep.PrefixLength.ValueInt64())
		filter.PrefixLength = &pl
	}
	out := &official.MACACLRuleEndpoint{}
	if err := out.FromMacAclRuleMacAddressEndpointFilter(filter); err != nil {
		diags.AddError("Failed to encode MAC endpoint filter", err.Error())
		return nil
	}
	return out
}

func expandProtocols(ctx context.Context, l types.List, diags *diag.Diagnostics) []official.IpAclRuleProtocolFilter {
	strs := listToStrings(ctx, l, diags)
	if strs == nil {
		return nil
	}
	out := make([]official.IpAclRuleProtocolFilter, 0, len(strs))
	for _, v := range strs {
		switch v {
		case "TCP", "UDP":
			out = append(out, official.IpAclRuleProtocolFilter(v))
		default:
			diags.AddError("Invalid protocol", v+" (want TCP or UDP)")
		}
	}
	return out
}

func listToStrings(ctx context.Context, l types.List, diags *diag.Diagnostics) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(l.ElementsAs(ctx, &out, false)...)
	return out
}

func listToInt32s(ctx context.Context, l types.List, diags *diag.Diagnostics) []int32 {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var ints []int64
	diags.Append(l.ElementsAs(ctx, &ints, false)...)
	out := make([]int32, 0, len(ints))
	for _, v := range ints {
		out = append(out, safeInt32(v))
	}
	return out
}
