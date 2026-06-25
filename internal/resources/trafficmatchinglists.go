package resources

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	_ resource.Resource                = &trafficMatchingListResource{}
	_ resource.ResourceWithConfigure   = &trafficMatchingListResource{}
	_ resource.ResourceWithImportState = &trafficMatchingListResource{}
)

// NewTrafficMatchingListResource returns the unifi_traffic_matching_list resource
// (a named list of IPv4, IPv6, or port matchers referenced by firewall policies).
func NewTrafficMatchingListResource() resource.Resource {
	return &trafficMatchingListResource{}
}

type trafficMatchingListResource struct {
	data *providerdata.Data
}

// trafficMatchingListModel is the whole list. The item shape is uniform across the
// three list kinds: match_type selects the variant, and value/start/stop carry the
// data. For PORTS lists value/start/stop hold integer port numbers as strings.
type trafficMatchingListModel struct {
	ID    types.String                   `tfsdk:"id"`
	Name  types.String                   `tfsdk:"name"`
	Type  types.String                   `tfsdk:"type"`
	Items []trafficMatchingListItemModel `tfsdk:"items"`
}

type trafficMatchingListItemModel struct {
	MatchType types.String `tfsdk:"match_type"`
	Value     types.String `tfsdk:"value"`
	Start     types.String `tfsdk:"start"`
	Stop      types.String `tfsdk:"stop"`
}

func (r *trafficMatchingListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_traffic_matching_list"
}

func (r *trafficMatchingListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A traffic matching list: a named, reusable set of IPv4, IPv6, or port matchers " +
			"that firewall policies can reference.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Traffic matching list UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "List name.",
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "List kind: IPV4_ADDRESSES, IPV6_ADDRESSES, or PORTS. Changing it " +
					"replaces the list.",
				Validators: []validator.String{
					stringvalidator.OneOf("IPV4_ADDRESSES", "IPV6_ADDRESSES", "PORTS"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"items": schema.ListNestedAttribute{
				Required:    true,
				Description: "Matchers in the list. Allowed match_type and fields depend on the list type.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"match_type": schema.StringAttribute{
							Required: true,
							Description: "Matcher variant. IPV4: IP_ADDRESS, IP_ADDRESS_RANGE, SUBNET. " +
								"IPV6: IP_ADDRESS, SUBNET. PORTS: PORT_NUMBER, PORT_NUMBER_RANGE.",
							Validators: []validator.String{
								stringvalidator.OneOf("IP_ADDRESS", "IP_ADDRESS_RANGE", "SUBNET", "PORT_NUMBER", "PORT_NUMBER_RANGE"),
							},
						},
						"value": schema.StringAttribute{
							Optional: true,
							Description: "Single value: an address (IP_ADDRESS), a CIDR (SUBNET), or a " +
								"port number (PORT_NUMBER).",
						},
						"start": schema.StringAttribute{
							Optional:    true,
							Description: "Range start, for IP_ADDRESS_RANGE / PORT_NUMBER_RANGE.",
						},
						"stop": schema.StringAttribute{
							Optional:    true,
							Description: "Range stop, for IP_ADDRESS_RANGE / PORT_NUMBER_RANGE.",
						},
					},
				},
			},
		},
	}
}

func (r *trafficMatchingListResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *trafficMatchingListResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan trafficMatchingListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandTrafficMatchingList(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().TrafficMatchingLists().Create(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create traffic matching list", err.Error())
		return
	}
	plan.ID = types.StringValue(got.Id.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *trafficMatchingListResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state trafficMatchingListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid traffic matching list id", err.Error())
		return
	}
	got, err := r.data.Client.Official().TrafficMatchingLists().Get(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read traffic matching list", err.Error())
		return
	}
	newState, diags := flattenTrafficMatchingList(got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *trafficMatchingListResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan trafficMatchingListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid traffic matching list id", err.Error())
		return
	}
	body, diags := expandTrafficMatchingList(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.data.Client.Official().TrafficMatchingLists().Update(ctx, r.data.SiteID, id, body); err != nil {
		resp.Diagnostics.AddError("Failed to update traffic matching list", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *trafficMatchingListResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state trafficMatchingListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid traffic matching list id", err.Error())
		return
	}
	if err := r.data.Client.Official().TrafficMatchingLists().Delete(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete traffic matching list", err.Error())
	}
}

func (r *trafficMatchingListResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandTrafficMatchingList builds the create/update body.
//
// TrafficMatchingListCreateOrUpdate is a discriminated union: its MarshalJSON
// emits the union body first, then OVERRIDES "name" and "type" with the outer
// named fields. "items" lives ONLY on the variant, so it must ride the union via
// From<Variant>. We therefore: build the variant (carrying items), call
// From<Variant> (which sets the union AND the discriminator), then set the outer
// Name directly so its override is non-empty.
func expandTrafficMatchingList(m trafficMatchingListModel) (official.TrafficMatchingListCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body official.TrafficMatchingListCreateOrUpdate

	switch m.Type.ValueString() {
	case "IPV4_ADDRESSES":
		items := expandIPv4Items(m.Items, &diags)
		v := official.IpV4TrafficMatchingListCreateUpdate{Name: m.Name.ValueString(), Type: "IPV4_ADDRESSES"}
		if len(items) > 0 {
			v.Items = &items
		}
		if err := body.FromIpV4TrafficMatchingListCreateUpdate(v); err != nil {
			diags.AddError("Failed to encode IPv4 matching list", err.Error())
		}
	case "IPV6_ADDRESSES":
		items := expandIPv6Items(m.Items, &diags)
		v := official.IpV6TrafficMatchingListCreateUpdate{Name: m.Name.ValueString(), Type: "IPV6_ADDRESSES"}
		if len(items) > 0 {
			v.Items = &items
		}
		if err := body.FromIpV6TrafficMatchingListCreateUpdate(v); err != nil {
			diags.AddError("Failed to encode IPv6 matching list", err.Error())
		}
	case "PORTS":
		items := expandPortItems(m.Items, &diags)
		v := official.PortTrafficMatchingListCreateUpdate{Name: m.Name.ValueString(), Type: "PORTS"}
		if len(items) > 0 {
			v.Items = &items
		}
		if err := body.FromPortTrafficMatchingListCreateUpdate(v); err != nil {
			diags.AddError("Failed to encode port matching list", err.Error())
		}
	default:
		diags.AddError("Unsupported list type", m.Type.ValueString())
	}

	// The union's "name" gets overridden by the outer Name in MarshalJSON, so set
	// it directly. (Type is already set by the From<Variant> call above and would
	// be re-emitted from the same value here.)
	body.Name = m.Name.ValueString()
	return body, diags
}

func expandIPv4Items(items []trafficMatchingListItemModel, diags *diag.Diagnostics) []official.IPv4Matching {
	out := make([]official.IPv4Matching, 0, len(items))
	for _, it := range items {
		var m official.IPv4Matching
		switch it.MatchType.ValueString() {
		case "IP_ADDRESS":
			val := it.Value.ValueString()
			if err := m.FromAddressIPv4Matching(official.AddressIPv4Matching{Type: "IP_ADDRESS", Value: &val}); err != nil {
				diags.AddError("Failed to encode IPv4 address matcher", err.Error())
			}
		case "IP_ADDRESS_RANGE":
			start, stop := it.Start.ValueString(), it.Stop.ValueString()
			if err := m.FromAddressRangeIPv4Matching(official.AddressRangeIPv4Matching{Type: "IP_ADDRESS_RANGE", Start: &start, Stop: &stop}); err != nil {
				diags.AddError("Failed to encode IPv4 range matcher", err.Error())
			}
		case "SUBNET":
			val := it.Value.ValueString()
			if err := m.FromSubnetIPv4Matching(official.SubnetIPv4Matching{Type: "SUBNET", Value: &val}); err != nil {
				diags.AddError("Failed to encode IPv4 subnet matcher", err.Error())
			}
		default:
			diags.AddError("Invalid IPv4 match_type", it.MatchType.ValueString()+" (want IP_ADDRESS, IP_ADDRESS_RANGE, or SUBNET)")
			continue
		}
		out = append(out, m)
	}
	return out
}

func expandIPv6Items(items []trafficMatchingListItemModel, diags *diag.Diagnostics) []official.IPv6Matching {
	out := make([]official.IPv6Matching, 0, len(items))
	for _, it := range items {
		var m official.IPv6Matching
		switch it.MatchType.ValueString() {
		case "IP_ADDRESS":
			val := it.Value.ValueString()
			if err := m.FromAddressIPv6Matching(official.AddressIPv6Matching{Type: "IP_ADDRESS", Value: &val}); err != nil {
				diags.AddError("Failed to encode IPv6 address matcher", err.Error())
			}
		case "SUBNET":
			val := it.Value.ValueString()
			if err := m.FromSubnetIPv6Matching(official.SubnetIPv6Matching{Type: "SUBNET", Value: &val}); err != nil {
				diags.AddError("Failed to encode IPv6 subnet matcher", err.Error())
			}
		default:
			diags.AddError("Invalid IPv6 match_type", it.MatchType.ValueString()+" (want IP_ADDRESS or SUBNET)")
			continue
		}
		out = append(out, m)
	}
	return out
}

func expandPortItems(items []trafficMatchingListItemModel, diags *diag.Diagnostics) []official.PortMatching {
	out := make([]official.PortMatching, 0, len(items))
	for _, it := range items {
		var m official.PortMatching
		switch it.MatchType.ValueString() {
		case "PORT_NUMBER":
			val, ok := parsePort(it.Value.ValueString(), "value", diags)
			if !ok {
				continue
			}
			if err := m.FromNumberPortMatching(official.NumberPortMatching{Type: "PORT_NUMBER", Value: &val}); err != nil {
				diags.AddError("Failed to encode port number matcher", err.Error())
			}
		case "PORT_NUMBER_RANGE":
			start, ok1 := parsePort(it.Start.ValueString(), "start", diags)
			stop, ok2 := parsePort(it.Stop.ValueString(), "stop", diags)
			if !ok1 || !ok2 {
				continue
			}
			if err := m.FromNumberRangePortMatching(official.NumberRangePortMatching{Type: "PORT_NUMBER_RANGE", Start: &start, Stop: &stop}); err != nil {
				diags.AddError("Failed to encode port range matcher", err.Error())
			}
		default:
			diags.AddError("Invalid port match_type", it.MatchType.ValueString()+" (want PORT_NUMBER or PORT_NUMBER_RANGE)")
			continue
		}
		out = append(out, m)
	}
	return out
}

func parsePort(s, field string, diags *diag.Diagnostics) (int32, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		diags.AddError("Invalid port "+field, fmt.Sprintf("%q is not an integer: %s", s, err.Error()))
		return 0, false
	}
	if n < 1 || n > 65535 {
		diags.AddError("Port out of range", fmt.Sprintf("%s = %d, want 1-65535", field, n))
		return 0, false
	}
	return int32(n), true
}

// flattenTrafficMatchingList reads the union back into state. The top-level
// discriminator selects which matcher slice to walk, and each item's own
// discriminator selects which fields to surface.
func flattenTrafficMatchingList(t *official.TrafficMatchingList) (trafficMatchingListModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := trafficMatchingListModel{
		ID:   types.StringValue(t.Id.String()),
		Name: types.StringValue(t.Name),
		Type: types.StringValue(t.Type),
	}

	switch t.Type {
	case "IPV4_ADDRESSES":
		v, err := t.AsIpV4TrafficMatchingList()
		if err != nil {
			diags.AddError("Failed to decode IPv4 matching list", err.Error())
			return m, diags
		}
		m.Items = flattenIPv4Items(v.Items, &diags)
	case "IPV6_ADDRESSES":
		v, err := t.AsIpV6TrafficMatchingList()
		if err != nil {
			diags.AddError("Failed to decode IPv6 matching list", err.Error())
			return m, diags
		}
		m.Items = flattenIPv6Items(v.Items, &diags)
	case "PORTS":
		v, err := t.AsPortTrafficMatchingList()
		if err != nil {
			diags.AddError("Failed to decode port matching list", err.Error())
			return m, diags
		}
		m.Items = flattenPortItems(v.Items, &diags)
	default:
		diags.AddError("Unsupported list type from API", t.Type)
	}
	return m, diags
}

func flattenIPv4Items(items *[]official.IPv4Matching, diags *diag.Diagnostics) []trafficMatchingListItemModel {
	if items == nil {
		return nil
	}
	out := make([]trafficMatchingListItemModel, 0, len(*items))
	for _, it := range *items {
		disc, err := it.Discriminator()
		if err != nil {
			diags.AddError("Failed to read IPv4 matcher type", err.Error())
			continue
		}
		row := trafficMatchingListItemModel{MatchType: types.StringValue(disc)}
		switch disc {
		case "IP_ADDRESS":
			if v, err := it.AsAddressIPv4Matching(); err == nil {
				row.Value = optString(v.Value)
			}
		case "SUBNET":
			if v, err := it.AsSubnetIPv4Matching(); err == nil {
				row.Value = optString(v.Value)
			}
		case "IP_ADDRESS_RANGE":
			if v, err := it.AsAddressRangeIPv4Matching(); err == nil {
				row.Start = optString(v.Start)
				row.Stop = optString(v.Stop)
			}
		}
		out = append(out, row)
	}
	return out
}

func flattenIPv6Items(items *[]official.IPv6Matching, diags *diag.Diagnostics) []trafficMatchingListItemModel {
	if items == nil {
		return nil
	}
	out := make([]trafficMatchingListItemModel, 0, len(*items))
	for _, it := range *items {
		disc, err := it.Discriminator()
		if err != nil {
			diags.AddError("Failed to read IPv6 matcher type", err.Error())
			continue
		}
		row := trafficMatchingListItemModel{MatchType: types.StringValue(disc)}
		switch disc {
		case "IP_ADDRESS":
			if v, err := it.AsAddressIPv6Matching(); err == nil {
				row.Value = optString(v.Value)
			}
		case "SUBNET":
			if v, err := it.AsSubnetIPv6Matching(); err == nil {
				row.Value = optString(v.Value)
			}
		}
		out = append(out, row)
	}
	return out
}

func flattenPortItems(items *[]official.PortMatching, diags *diag.Diagnostics) []trafficMatchingListItemModel {
	if items == nil {
		return nil
	}
	out := make([]trafficMatchingListItemModel, 0, len(*items))
	for _, it := range *items {
		disc, err := it.Discriminator()
		if err != nil {
			diags.AddError("Failed to read port matcher type", err.Error())
			continue
		}
		row := trafficMatchingListItemModel{MatchType: types.StringValue(disc)}
		switch disc {
		case "PORT_NUMBER":
			if v, err := it.AsNumberPortMatching(); err == nil {
				row.Value = optInt32(v.Value)
			}
		case "PORT_NUMBER_RANGE":
			if v, err := it.AsNumberRangePortMatching(); err == nil {
				row.Start = optInt32(v.Start)
				row.Stop = optInt32(v.Stop)
			}
		}
		out = append(out, row)
	}
	return out
}

func optString(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func optInt32(p *int32) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(strconv.Itoa(int(*p)))
}
