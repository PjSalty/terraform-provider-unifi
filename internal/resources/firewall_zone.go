package resources

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

var (
	_ resource.Resource                = &firewallZoneResource{}
	_ resource.ResourceWithConfigure   = &firewallZoneResource{}
	_ resource.ResourceWithImportState = &firewallZoneResource{}
)

// NewFirewallZoneResource returns the unifi_firewall_zone resource (a zone-based
// firewall zone grouping one or more networks).
func NewFirewallZoneResource() resource.Resource {
	return &firewallZoneResource{}
}

type firewallZoneResource struct {
	data *providerdata.Data
}

type firewallZoneModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	NetworkIDs types.Set    `tfsdk:"network_ids"`
}

func (r *firewallZoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_zone"
}

func (r *firewallZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A zone-based firewall zone on the UniFi controller. A zone groups " +
			"one or more networks so firewall policies can reference traffic between zones.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Firewall zone UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Firewall zone name.",
			},
			"network_ids": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "UUIDs of the networks that belong to this zone.",
			},
		},
	}
}

func (r *firewallZoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *firewallZoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan firewallZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandFirewallZone(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Firewall().CreateZone(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create firewall zone", err.Error())
		return
	}
	state, diags := flattenFirewallZone(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallZoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall zone id", err.Error())
		return
	}
	got, err := r.data.Client.Official().Firewall().GetZone(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read firewall zone", err.Error())
		return
	}
	state, diags := flattenFirewallZone(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallZoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan firewallZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall zone id", err.Error())
		return
	}
	body, diags := expandFirewallZone(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Firewall().UpdateZone(ctx, r.data.SiteID, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update firewall zone", err.Error())
		return
	}
	state, diags := flattenFirewallZone(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallZoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state firewallZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall zone id", err.Error())
		return
	}
	// The controller refuses to delete system-defined zones (origin SYSTEM, e.g.
	// Internal/External/Gateway); that error is surfaced as-is. ErrNotFound means
	// the zone is already gone, which is success.
	if err := r.data.Client.Official().Firewall().DeleteZone(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete firewall zone", err.Error())
	}
}

func (r *firewallZoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandFirewallZone builds the create/update body. FirewallZoneCreateOrUpdate is
// a plain struct (no discriminated union), so the named fields are set directly.
func expandFirewallZone(ctx context.Context, m firewallZoneModel) (official.FirewallZoneCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics
	ids := setToUUIDs(ctx, m.NetworkIDs, &diags)
	if ids == nil {
		// The API field is a non-omitempty slice; send an empty list rather than
		// null when no networks are bound.
		ids = []uuid.UUID{}
	}
	return official.FirewallZoneCreateOrUpdate{
		Name:       m.Name.ValueString(),
		NetworkIds: ids,
	}, diags
}

func flattenFirewallZone(ctx context.Context, z *official.FirewallZone) (firewallZoneModel, diag.Diagnostics) {
	ids := make([]string, 0, len(z.NetworkIds))
	for _, id := range z.NetworkIds {
		ids = append(ids, id.String())
	}
	set, diags := types.SetValueFrom(ctx, types.StringType, ids)
	return firewallZoneModel{
		ID:         types.StringValue(z.Id.String()),
		Name:       types.StringValue(z.Name),
		NetworkIDs: set,
	}, diags
}
