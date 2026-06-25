package resources

import (
	"context"
	"errors"
	"fmt"

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
	_ resource.Resource                = &wifiBroadcastResource{}
	_ resource.ResourceWithConfigure   = &wifiBroadcastResource{}
	_ resource.ResourceWithImportState = &wifiBroadcastResource{}
)

// NewWifiBroadcastResource returns the unifi_wifi_broadcast resource (an SSID).
func NewWifiBroadcastResource() resource.Resource {
	return &wifiBroadcastResource{}
}

type wifiBroadcastResource struct {
	data *providerdata.Data
}

type wifiBroadcastModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	HideName        types.Bool   `tfsdk:"hide_name"`
	Security        types.String `tfsdk:"security"`
	Passphrase      types.String `tfsdk:"passphrase"`
	PmfMode         types.String `tfsdk:"pmf_mode"`
	Frequencies     types.Set    `tfsdk:"broadcasting_frequencies_ghz"`
	NetworkID       types.String `tfsdk:"network_id"`
	DeviceFilter    types.Set    `tfsdk:"broadcasting_device_filter"`
	ClientIsolation types.Bool   `tfsdk:"client_isolation_enabled"`
}

func (r *wifiBroadcastResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_broadcast"
}

func (r *wifiBroadcastResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A WiFi broadcast (SSID) on the UniFi controller.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "WiFi broadcast UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "SSID name.",
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the SSID is enabled.",
			},
			"hide_name": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Hide the SSID (do not broadcast the name).",
			},
			"security": schema.StringAttribute{
				Required:    true,
				Description: "Security mode: OPEN, WPA2_PERSONAL, WPA3_PERSONAL, or WPA2_WPA3_PERSONAL.",
				Validators: []validator.String{
					stringvalidator.OneOf("OPEN", "WPA2_PERSONAL", "WPA3_PERSONAL", "WPA2_WPA3_PERSONAL"),
				},
			},
			"passphrase": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Pre-shared key (8-63 chars). Required for the personal security modes; omit for OPEN.",
			},
			"pmf_mode": schema.StringAttribute{
				Optional:    true,
				Description: "Protected Management Frames: REQUIRED or OPTIONAL. WPA3 implies required.",
				Validators:  []validator.String{stringvalidator.OneOf("REQUIRED", "OPTIONAL")},
			},
			"broadcasting_frequencies_ghz": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Frequency bands to advertise on: any of \"2.4\", \"5\", \"6\".",
			},
			"network_id": schema.StringAttribute{
				Optional:    true,
				Description: "UUID of the network (VLAN) to bind clients to. Omit for the native network.",
			},
			"broadcasting_device_filter": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "UUIDs of the APs that broadcast this SSID. Omit to broadcast on all APs.",
			},
			"client_isolation_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Prevent clients on this SSID from reaching each other.",
			},
		},
	}
}

func (r *wifiBroadcastResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *wifiBroadcastResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan wifiBroadcastModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandWifiBroadcast(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().WifiBroadcasts().Create(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create SSID", err.Error())
		return
	}
	plan.ID = types.StringValue(got.Id.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *wifiBroadcastResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wifiBroadcastModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSID id", err.Error())
		return
	}
	got, err := r.data.Client.Official().WifiBroadcasts().Get(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read SSID", err.Error())
		return
	}
	// Refresh the reliably-returned top-level fields. shortcut: the nested-union
	// fields (security, passphrase, frequencies, network_id, device_filter) stay
	// from prior state. passphrase is write-only on the API; full union round-trip
	// refresh of the others is added with the acceptance tests (Step 6) against a
	// live controller, where the As* navigation can be verified.
	state.Name = types.StringValue(got.Name)
	state.Enabled = types.BoolValue(got.Enabled)
	state.HideName = types.BoolValue(got.HideName)
	state.ClientIsolation = types.BoolValue(got.ClientIsolationEnabled)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *wifiBroadcastResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan wifiBroadcastModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSID id", err.Error())
		return
	}
	body, diags := expandWifiBroadcast(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.data.Client.Official().WifiBroadcasts().Update(ctx, r.data.SiteID, id, body); err != nil {
		resp.Diagnostics.AddError("Failed to update SSID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *wifiBroadcastResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state wifiBroadcastModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSID id", err.Error())
		return
	}
	if err := r.data.Client.Official().WifiBroadcasts().Delete(ctx, r.data.SiteID, id, nil); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete SSID", err.Error())
	}
}

func (r *wifiBroadcastResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandWifiBroadcast builds the create/update body. The top-level union's named
// fields override the union in MarshalJSON, so we set them directly and use the
// STANDARD variant only for the band list, which lives solely on the variant.
func expandWifiBroadcast(ctx context.Context, m wifiBroadcastModel) (official.WifiBroadcastCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics

	sec, secDiags := expandSecurity(m)
	diags.Append(secDiags...)

	body := official.WifiBroadcastCreateOrUpdate{
		Name:                   m.Name.ValueString(),
		Enabled:                m.Enabled.ValueBool(),
		HideName:               m.HideName.ValueBool(),
		ClientIsolationEnabled: m.ClientIsolation.ValueBool(),
		SecurityConfiguration:  sec,
		Type:                   "STANDARD",
	}

	if !m.NetworkID.IsNull() && m.NetworkID.ValueString() != "" {
		nid, err := uuid.Parse(m.NetworkID.ValueString())
		if err != nil {
			diags.AddError("Invalid network_id", err.Error())
		} else {
			ref := &official.WifiNetworkReference{}
			if err := ref.FromWifiSpecificNetwork(official.WifiSpecificNetwork{Type: "SPECIFIC", NetworkId: &nid}); err != nil {
				diags.AddError("Failed to encode network binding", err.Error())
			} else {
				body.Network = ref
			}
		}
	}

	if devs := setToUUIDs(ctx, m.DeviceFilter, &diags); len(devs) > 0 {
		f := &official.BroadcastingDeviceFilter{}
		if err := f.FromWifiDevicesFilter(official.WifiDevicesFilter{Type: "DEVICES", DeviceIds: &devs}); err != nil {
			diags.AddError("Failed to encode device filter", err.Error())
		} else {
			body.BroadcastingDeviceFilter = f
		}
	}

	// The band list lives only on the STANDARD variant, so it rides the union;
	// the named fields above override the union for everything they also carry.
	std := official.StandardWifiBroadcastCreateUpdate{Type: "STANDARD"}
	if freqs := setToFrequencies(ctx, m.Frequencies, &diags); len(freqs) > 0 {
		std.BroadcastingFrequenciesGHz = &freqs
	}
	if err := body.FromStandardWifiBroadcastCreateUpdate(std); err != nil {
		diags.AddError("Failed to encode SSID body", err.Error())
	}

	return body, diags
}

func expandSecurity(m wifiBroadcastModel) (official.WifiSecurityConfigurationDetailObject, diag.Diagnostics) {
	var sec official.WifiSecurityConfigurationDetailObject
	var diags diag.Diagnostics
	pass := m.Passphrase.ValueString()
	var err error

	switch m.Security.ValueString() {
	case "OPEN":
		err = sec.FromWifiOpenSecurityConfigurationDetail(official.WifiOpenSecurityConfigurationDetail{Type: "OPEN"})
	case "WPA2_PERSONAL":
		v := official.WifiWpa2PersonalSecurityConfigurationDetail{Type: "WPA2_PERSONAL", Passphrase: &pass}
		if !m.PmfMode.IsNull() && m.PmfMode.ValueString() != "" {
			pm := official.WifiWpa2PersonalSecurityConfigurationDetailPmfMode(m.PmfMode.ValueString())
			v.PmfMode = &pm
		}
		err = sec.FromWifiWpa2PersonalSecurityConfigurationDetail(v)
	case "WPA3_PERSONAL":
		err = sec.FromWifiWpa3PersonalSecurityConfigurationDetail(
			official.WifiWpa3PersonalSecurityConfigurationDetail{Type: "WPA3_PERSONAL", Passphrase: &pass})
	case "WPA2_WPA3_PERSONAL":
		v := official.WifiWpa2Wpa3PersonalSecurityConfigurationDetail{Type: "WPA2_WPA3_PERSONAL", Passphrase: &pass}
		if !m.PmfMode.IsNull() && m.PmfMode.ValueString() != "" {
			pm := official.WifiWpa2Wpa3PersonalSecurityConfigurationDetailPmfMode(m.PmfMode.ValueString())
			v.PmfMode = &pm
		}
		err = sec.FromWifiWpa2Wpa3PersonalSecurityConfigurationDetail(v)
	default:
		diags.AddError("Unsupported security mode", m.Security.ValueString())
	}
	if err != nil {
		diags.AddError("Failed to encode security configuration", err.Error())
	}
	return sec, diags
}

func setToUUIDs(ctx context.Context, s types.Set, diags *diag.Diagnostics) []uuid.UUID {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var strs []string
	diags.Append(s.ElementsAs(ctx, &strs, false)...)
	out := make([]uuid.UUID, 0, len(strs))
	for _, v := range strs {
		id, err := uuid.Parse(v)
		if err != nil {
			diags.AddError("Invalid UUID", v)
			continue
		}
		out = append(out, id)
	}
	return out
}

func setToFrequencies(ctx context.Context, s types.Set, diags *diag.Diagnostics) []official.StandardWifiBroadcastCreateUpdateBroadcastingFrequenciesGHz {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var strs []string
	diags.Append(s.ElementsAs(ctx, &strs, false)...)
	out := make([]official.StandardWifiBroadcastCreateUpdateBroadcastingFrequenciesGHz, 0, len(strs))
	for _, v := range strs {
		switch v {
		case "2.4":
			out = append(out, official.StandardWifiBroadcastCreateUpdateBroadcastingFrequenciesGHzN24)
		case "5":
			out = append(out, official.StandardWifiBroadcastCreateUpdateBroadcastingFrequenciesGHzN5)
		case "6":
			out = append(out, official.StandardWifiBroadcastCreateUpdateBroadcastingFrequenciesGHzN6)
		default:
			diags.AddError("Invalid frequency", v+" (want 2.4, 5, or 6)")
		}
	}
	return out
}
