package resources

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                = &networkResource{}
	_ resource.ResourceWithConfigure   = &networkResource{}
	_ resource.ResourceWithImportState = &networkResource{}
)

// NewNetworkResource returns the unifi_network resource (a VLAN-only network).
func NewNetworkResource() resource.Resource {
	return &networkResource{}
}

type networkResource struct {
	data *providerdata.Data
}

type networkModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	VlanID  types.Int64  `tfsdk:"vlan_id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A VLAN-only network (an 802.1Q tag) on the UniFi controller.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Network UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Network name.",
			},
			"vlan_id": schema.Int64Attribute{
				Required:    true,
				Description: "VLAN ID. 1 for the default network, >= 2 for additional networks.",
				Validators:  []validator.Int64{int64validator.Between(1, 4009)},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the network is enabled.",
			},
		},
	}
}

func (r *networkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *networkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Networks().Create(ctx, r.data.SiteID, expandNetwork(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to create network", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenNetwork(got))...)
}

func (r *networkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid network id", err.Error())
		return
	}
	got, err := r.data.Client.Official().Networks().Get(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read network", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenNetwork(got))...)
}

func (r *networkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid network id", err.Error())
		return
	}
	got, err := r.data.Client.Official().Networks().Update(ctx, r.data.SiteID, id, expandNetwork(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to update network", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenNetwork(got))...)
}

func (r *networkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid network id", err.Error())
		return
	}
	// The controller refuses to delete the default network; that error is
	// surfaced as-is. ErrNotFound means it is already gone, which is success.
	if err := r.data.Client.Official().Networks().Delete(ctx, r.data.SiteID, id, nil); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete network", err.Error())
	}
}

func (r *networkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandNetwork builds the create/update body for a VLAN-only network.
//
// In NetworkCreateOrUpdate.MarshalJSON the named fields are written AFTER (and
// therefore override) the union, so for an UNMANAGED network we set the named
// fields directly. Calling FromUnmanagedNetworkCreateUpdate alone would only
// populate the union and leave the named fields zero, which the marshaler would
// then emit as empty.
func expandNetwork(m networkModel) official.NetworkCreateOrUpdate {
	return official.NetworkCreateOrUpdate{
		Name:       m.Name.ValueString(),
		VlanId:     safeInt32(m.VlanID.ValueInt64()),
		Enabled:    m.Enabled.ValueBool(),
		Management: "UNMANAGED",
	}
}

func flattenNetwork(n *official.NetworkDetails) networkModel {
	return networkModel{
		ID:      types.StringValue(n.Id.String()),
		Name:    types.StringValue(n.Name),
		VlanID:  types.Int64Value(int64(n.VlanId)),
		Enabled: types.BoolValue(n.Enabled),
	}
}
