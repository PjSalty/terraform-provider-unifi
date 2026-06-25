package resources

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int32validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
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
	_ resource.Resource                = &hotspotVoucherResource{}
	_ resource.ResourceWithConfigure   = &hotspotVoucherResource{}
	_ resource.ResourceWithImportState = &hotspotVoucherResource{}
)

// NewHotspotVoucherResource returns the unifi_hotspot_voucher resource.
//
// One resource represents a single batch of hotspot vouchers created in one
// CreateVouchers call: every voucher in the batch shares the same name and
// limits, but each gets its own UUID and printable code. The Official API has
// no voucher-update endpoint, so every configurable attribute forces
// replacement.
func NewHotspotVoucherResource() resource.Resource {
	return &hotspotVoucherResource{}
}

type hotspotVoucherResource struct {
	data *providerdata.Data
}

type hotspotVoucherModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Count                types.Int32  `tfsdk:"count"`
	TimeLimitMinutes     types.Int64  `tfsdk:"time_limit_minutes"`
	AuthorizedGuestLimit types.Int64  `tfsdk:"authorized_guest_limit"`
	DataUsageLimitMBytes types.Int64  `tfsdk:"data_usage_limit_mbytes"`
	RxRateLimitKbps      types.Int64  `tfsdk:"rx_rate_limit_kbps"`
	TxRateLimitKbps      types.Int64  `tfsdk:"tx_rate_limit_kbps"`
	Vouchers             types.List   `tfsdk:"vouchers"`
}

// voucherObjectType is the element type of the computed vouchers list. It is
// the read-back projection of one HotspotVoucherDetails.
var voucherObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":                     types.StringType,
	"code":                   types.StringType,
	"name":                   types.StringType,
	"expired":                types.BoolType,
	"authorized_guest_count": types.Int64Type,
	"created_at":             types.StringType,
	"expires_at":             types.StringType,
	"activated_at":           types.StringType,
}}

func (r *hotspotVoucherResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hotspot_voucher"
}

func (r *hotspotVoucherResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	// All inputs force replacement: the Official API can only create and delete
	// vouchers, never update them.
	replaceInt64 := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A batch of UniFi hotspot guest vouchers. Every voucher in the batch " +
			"shares the same note and limits; the controller assigns each its own code. " +
			"Vouchers cannot be updated in place, so any change forces a new batch.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "UUID of the first voucher in the batch; used as the resource identity.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Voucher note, duplicated across all generated vouchers.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"count": schema.Int32Attribute{
				Optional:      true,
				Computed:      true,
				Default:       int32default.StaticInt32(1),
				Description:   "Number of vouchers to generate (1-1000).",
				Validators:    []validator.Int32{int32validator.Between(1, 1000)},
				PlanModifiers: []planmodifier.Int32{int32planmodifier.RequiresReplace()},
			},
			"time_limit_minutes": schema.Int64Attribute{
				Required: true,
				Description: "How long (in minutes) the voucher grants access, counted from the " +
					"first guest's authorization (1-1000000).",
				Validators:    []validator.Int64{int64validator.Between(1, 1000000)},
				PlanModifiers: replaceInt64,
			},
			"authorized_guest_limit": schema.Int64Attribute{
				Optional:      true,
				Description:   "Limit on how many different guests can use each voucher. Omit for unlimited.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: replaceInt64,
			},
			"data_usage_limit_mbytes": schema.Int64Attribute{
				Optional:      true,
				Description:   "Per-voucher data usage limit in megabytes (1-1048576). Omit for unlimited.",
				Validators:    []validator.Int64{int64validator.Between(1, 1048576)},
				PlanModifiers: replaceInt64,
			},
			"rx_rate_limit_kbps": schema.Int64Attribute{
				Optional:      true,
				Description:   "Download rate limit in kilobits per second (2-100000). Omit for unlimited.",
				Validators:    []validator.Int64{int64validator.Between(2, 100000)},
				PlanModifiers: replaceInt64,
			},
			"tx_rate_limit_kbps": schema.Int64Attribute{
				Optional:      true,
				Description:   "Upload rate limit in kilobits per second (2-100000). Omit for unlimited.",
				Validators:    []validator.Int64{int64validator.Between(2, 100000)},
				PlanModifiers: replaceInt64,
			},
			"vouchers": schema.ListAttribute{
				Computed:    true,
				ElementType: voucherObjectType,
				Description: "The generated vouchers, including their printable codes.",
			},
		},
	}
}

func (r *hotspotVoucherResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *hotspotVoucherResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan hotspotVoucherModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Hotspot().CreateVouchers(ctx, r.data.SiteID, expandHotspotVoucher(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to create hotspot vouchers", err.Error())
		return
	}
	if got.Vouchers == nil || len(*got.Vouchers) == 0 {
		resp.Diagnostics.AddError("Hotspot voucher creation returned no vouchers",
			"The controller accepted the request but returned an empty voucher set.")
		return
	}
	model, diags := flattenHotspotVouchers(ctx, plan, *got.Vouchers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *hotspotVoucherResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state hotspotVoucherModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Re-fetch each voucher in the batch by ID. The primary voucher (the resource
	// id) disappearing means the batch is gone from the controller's view, so the
	// resource is removed and recreated. Individual non-primary vouchers that were
	// consumed/expired are simply dropped from the computed list.
	ids, diags := voucherIDsFromState(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	primary, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid hotspot voucher id", err.Error())
		return
	}
	live := make([]official.HotspotVoucherDetails, 0, len(ids))
	for _, id := range ids {
		got, err := r.data.Client.Official().Hotspot().GetVoucher(ctx, r.data.SiteID, id)
		if err != nil {
			if errors.Is(err, unifi.ErrNotFound) {
				if id == primary {
					resp.State.RemoveResource(ctx)
					return
				}
				continue
			}
			resp.Diagnostics.AddError("Failed to read hotspot voucher", err.Error())
			return
		}
		live = append(live, *got)
	}
	if len(live) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	model, diags := flattenHotspotVouchers(ctx, state, live)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// Update can never run: every schema attribute carries RequiresReplace, so the
// framework replaces the resource instead of calling Update. It is implemented
// to satisfy resource.Resource and guards read-only mode defensively.
func (r *hotspotVoucherResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	resp.Diagnostics.AddError("Hotspot vouchers cannot be updated",
		"The Official API has no voucher-update endpoint; changes force replacement. This is a provider bug if reached.")
}

func (r *hotspotVoucherResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state hotspotVoucherModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ids, diags := voucherIDsFromState(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Delete each voucher in the batch individually. ErrNotFound means it is
	// already gone, which is success.
	for _, id := range ids {
		if _, err := r.data.Client.Official().Hotspot().DeleteVoucher(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
			resp.Diagnostics.AddError("Failed to delete hotspot voucher", err.Error())
			return
		}
	}
}

func (r *hotspotVoucherResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import takes a single voucher UUID. Read reconstructs the rest of the
	// state from that one voucher.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandHotspotVoucher builds the CreateVouchers body. This is a plain mapping:
// the request struct has no discriminated union, so the named fields are set
// directly. Optional limits are sent as nil pointers when unset (omitempty),
// which the controller reads as "unlimited".
func expandHotspotVoucher(m hotspotVoucherModel) official.HotspotVoucherCreationRequest {
	body := official.HotspotVoucherCreationRequest{
		Name:             m.Name.ValueString(),
		TimeLimitMinutes: m.TimeLimitMinutes.ValueInt64(),
	}
	if !m.Count.IsNull() && !m.Count.IsUnknown() {
		c := m.Count.ValueInt32()
		body.Count = &c
	}
	if !m.AuthorizedGuestLimit.IsNull() {
		v := m.AuthorizedGuestLimit.ValueInt64()
		body.AuthorizedGuestLimit = &v
	}
	if !m.DataUsageLimitMBytes.IsNull() {
		v := m.DataUsageLimitMBytes.ValueInt64()
		body.DataUsageLimitMBytes = &v
	}
	if !m.RxRateLimitKbps.IsNull() {
		v := m.RxRateLimitKbps.ValueInt64()
		body.RxRateLimitKbps = &v
	}
	if !m.TxRateLimitKbps.IsNull() {
		v := m.TxRateLimitKbps.ValueInt64()
		body.TxRateLimitKbps = &v
	}
	return body
}

// flattenHotspotVouchers projects the live voucher set back into state. The
// scalar inputs are carried over from the plan/state (they are identical across
// the batch and the controller does not echo every one of them on each item),
// and the computed list captures each voucher's id, code and lifecycle fields.
// The resource id is the first voucher's UUID.
func flattenHotspotVouchers(ctx context.Context, in hotspotVoucherModel, vouchers []official.HotspotVoucherDetails) (hotspotVoucherModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := in
	out.ID = types.StringValue(vouchers[0].Id.String())
	out.Name = types.StringValue(vouchers[0].Name)
	out.Count = types.Int32Value(int32(len(vouchers)))
	out.TimeLimitMinutes = types.Int64Value(vouchers[0].TimeLimitMinutes)
	out.AuthorizedGuestLimit = optInt64(vouchers[0].AuthorizedGuestLimit)
	out.DataUsageLimitMBytes = optInt64(vouchers[0].DataUsageLimitMBytes)
	out.RxRateLimitKbps = optInt64(vouchers[0].RxRateLimitKbps)
	out.TxRateLimitKbps = optInt64(vouchers[0].TxRateLimitKbps)

	elems := make([]voucherObjModel, 0, len(vouchers))
	for _, v := range vouchers {
		elems = append(elems, voucherObjModel{
			ID:                   types.StringValue(v.Id.String()),
			Code:                 types.StringValue(v.Code),
			Name:                 types.StringValue(v.Name),
			Expired:              types.BoolValue(v.Expired),
			AuthorizedGuestCount: types.Int64Value(v.AuthorizedGuestCount),
			CreatedAt:            types.StringValue(v.CreatedAt.Format(time.RFC3339)),
			ExpiresAt:            optTime(v.ExpiresAt),
			ActivatedAt:          optTime(v.ActivatedAt),
		})
	}
	list, d := types.ListValueFrom(ctx, voucherObjectType, elems)
	diags.Append(d...)
	out.Vouchers = list
	return out, diags
}

// voucherObjModel is the Go shape of a single element of the vouchers list. Its
// tfsdk tags must match voucherObjectType.
type voucherObjModel struct {
	ID                   types.String `tfsdk:"id"`
	Code                 types.String `tfsdk:"code"`
	Name                 types.String `tfsdk:"name"`
	Expired              types.Bool   `tfsdk:"expired"`
	AuthorizedGuestCount types.Int64  `tfsdk:"authorized_guest_count"`
	CreatedAt            types.String `tfsdk:"created_at"`
	ExpiresAt            types.String `tfsdk:"expires_at"`
	ActivatedAt          types.String `tfsdk:"activated_at"`
}

// voucherIDsFromState extracts the UUIDs of every voucher in the batch from the
// computed vouchers list in state.
func voucherIDsFromState(ctx context.Context, state hotspotVoucherModel) ([]uuid.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	var elems []voucherObjModel
	if state.Vouchers.IsNull() || state.Vouchers.IsUnknown() {
		// Imported state has no list yet; fall back to the resource id alone.
		id, err := uuid.Parse(state.ID.ValueString())
		if err != nil {
			diags.AddError("Invalid hotspot voucher id", err.Error())
			return nil, diags
		}
		return []uuid.UUID{id}, diags
	}
	diags.Append(state.Vouchers.ElementsAs(ctx, &elems, false)...)
	if diags.HasError() {
		return nil, diags
	}
	ids := make([]uuid.UUID, 0, len(elems))
	for _, e := range elems {
		id, err := uuid.Parse(e.ID.ValueString())
		if err != nil {
			diags.AddError("Invalid hotspot voucher id", err.Error())
			return nil, diags
		}
		ids = append(ids, id)
	}
	return ids, diags
}

// optInt64 maps an optional API *int64 to a nullable framework Int64.
func optInt64(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

// optTime maps an optional API *time.Time to a nullable RFC3339 framework String.
func optTime(t *time.Time) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.Format(time.RFC3339))
}
