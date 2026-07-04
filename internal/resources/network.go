package resources

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

// Management modes. The controller's discriminator is "GATEWAY" (not
// "GATEWAY_MANAGED"); see FromGatewayManagedNetworkCreateUpdate in go-unifi.
const (
	mgmtUnmanaged  = "UNMANAGED"
	mgmtGateway    = "GATEWAY"
	dhcpModeServer = "SERVER"
)

var (
	_ resource.Resource                   = &networkResource{}
	_ resource.ResourceWithConfigure      = &networkResource{}
	_ resource.ResourceWithImportState    = &networkResource{}
	_ resource.ResourceWithValidateConfig = &networkResource{}
)

// NewNetworkResource returns the unifi_network resource. A network is either
// VLAN-only (management = UNMANAGED, an 802.1Q tag an external router handles)
// or gateway-managed (management = GATEWAY, the UniFi gateway routes it with an
// L3 subnet and optional DHCP server).
func NewNetworkResource() resource.Resource {
	return &networkResource{}
}

type networkResource struct {
	data *providerdata.Data
}

type networkModel struct {
	ID         types.String  `tfsdk:"id"`
	Name       types.String  `tfsdk:"name"`
	VlanID     types.Int64   `tfsdk:"vlan_id"`
	Enabled    types.Bool    `tfsdk:"enabled"`
	Management types.String  `tfsdk:"management"`
	Gateway    *gatewayModel `tfsdk:"gateway"`
}

// gatewayModel is the L3 config the UniFi gateway applies when management =
// GATEWAY. host_ip_address + prefix_length are the gateway's own address and
// the subnet size (e.g. 192.168.1.1 / 24).
type gatewayModel struct {
	HostIPAddress    types.String `tfsdk:"host_ip_address"`
	PrefixLength     types.Int64  `tfsdk:"prefix_length"`
	AutoScaleEnabled types.Bool   `tfsdk:"auto_scale_enabled"`
	DHCP             *dhcpModel   `tfsdk:"dhcp"`
}

// dhcpModel is a DHCP server on the gateway-managed subnet. Present block =>
// DHCP server on; omit the block for no DHCP. Mode is always SERVER (RELAY is a
// separate union variant, deferred).
type dhcpModel struct {
	RangeStart       types.String `tfsdk:"range_start"`
	RangeStop        types.String `tfsdk:"range_stop"`
	DNSServers       types.List   `tfsdk:"dns_servers"`
	DomainName       types.String `tfsdk:"domain_name"`
	LeaseTimeSeconds types.Int64  `tfsdk:"lease_time_seconds"`
}

func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A UniFi network: either VLAN-only (management = UNMANAGED) or gateway-managed " +
			"(management = GATEWAY, routed by the UniFi gateway with an L3 subnet and optional DHCP).",
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
			"management": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(mgmtUnmanaged),
				Description: "Management mode: UNMANAGED (VLAN-only, an external router does L3) or " +
					"GATEWAY (the UniFi gateway routes the network; requires a gateway block).",
				Validators: []validator.String{stringvalidator.OneOf(mgmtUnmanaged, mgmtGateway)},
			},
			"gateway": schema.SingleNestedAttribute{
				Optional: true,
				Description: "L3 configuration applied by the UniFi gateway. Required when " +
					"management = GATEWAY; must be omitted otherwise.",
				Attributes: map[string]schema.Attribute{
					"host_ip_address": schema.StringAttribute{
						Required:    true,
						Description: "The gateway's IP address on this network (e.g. 192.168.1.1).",
					},
					"prefix_length": schema.Int64Attribute{
						Required:    true,
						Description: "Subnet prefix length (8-30), e.g. 24 for a /24.",
						Validators:  []validator.Int64{int64validator.Between(8, 30)},
					},
					"auto_scale_enabled": schema.BoolAttribute{
						Optional:    true,
						Computed:    true,
						Default:     booldefault.StaticBool(false),
						Description: "Auto-scale the subnet size based on active DHCP leases.",
					},
					"dhcp": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "DHCP server for this subnet. Omit the block for no DHCP.",
						Attributes: map[string]schema.Attribute{
							"range_start": schema.StringAttribute{
								Required:    true,
								Description: "First address of the DHCP pool.",
							},
							"range_stop": schema.StringAttribute{
								Required:    true,
								Description: "Last address of the DHCP pool.",
							},
							"dns_servers": schema.ListAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "DNS servers handed to clients (max 4). Omit for the controller default.",
								Validators:  []validator.List{listvalidator.SizeAtMost(4)},
							},
							"domain_name": schema.StringAttribute{
								Optional:    true,
								Description: "Domain name handed to clients.",
							},
							"lease_time_seconds": schema.Int64Attribute{
								Optional:    true,
								Computed:    true,
								Description: "DHCP lease time in seconds (0-31536000).",
								Validators:  []validator.Int64{int64validator.Between(0, 31536000)},
							},
						},
					},
				},
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

// ValidateConfig enforces the mutual requirement between management mode and
// the gateway block at plan time. management defaults to UNMANAGED, so a null
// value here means UNMANAGED.
func (r *networkResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m networkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mgmt := m.Management.ValueString()
	if m.Management.IsNull() || m.Management.IsUnknown() {
		mgmt = mgmtUnmanaged
	}
	switch {
	case mgmt == mgmtGateway && m.Gateway == nil:
		resp.Diagnostics.AddAttributeError(path.Root("gateway"), "Missing gateway block",
			`management = "GATEWAY" requires a gateway { ... } block.`)
	case mgmt == mgmtUnmanaged && m.Gateway != nil:
		resp.Diagnostics.AddAttributeError(path.Root("gateway"), "Unexpected gateway block",
			`gateway { ... } is only valid when management = "GATEWAY".`)
	}
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
	body, diags := expandNetwork(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Networks().Create(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create network", err.Error())
		return
	}
	state, diags := flattenNetwork(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
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
	newState, diags := flattenNetwork(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
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
	body, diags := expandNetwork(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Networks().Update(ctx, r.data.SiteID, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update network", err.Error())
		return
	}
	state, diags := flattenNetwork(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
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

// expandNetwork builds the create/update body.
//
// NetworkCreateOrUpdate.MarshalJSON writes the named fields (name, vlanId,
// enabled, management) AFTER the union, overriding it, so those must be set on
// the outer struct or they marshal as zero values. For UNMANAGED that is the
// whole body. For GATEWAY the L3 config lives only in the union, so we call
// FromGatewayManagedNetworkCreateUpdate (which also sets management = GATEWAY)
// and still set the named fields.
func expandNetwork(ctx context.Context, m networkModel) (official.NetworkCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := official.NetworkCreateOrUpdate{
		Name:       m.Name.ValueString(),
		VlanId:     safeInt32(m.VlanID.ValueInt64()),
		Enabled:    m.Enabled.ValueBool(),
		Management: mgmtUnmanaged,
	}
	if m.Management.ValueString() != mgmtGateway {
		return body, diags
	}

	ipv4 := &official.GatewayManagedIPv4Configuration{
		HostIpAddress:    m.Gateway.HostIPAddress.ValueString(),
		PrefixLength:     safeInt32(m.Gateway.PrefixLength.ValueInt64()),
		AutoScaleEnabled: m.Gateway.AutoScaleEnabled.ValueBool(),
	}
	if d := m.Gateway.DHCP; d != nil {
		server := official.GatewayManagedIPv4DHCPServerConfiguration{
			Mode:           dhcpModeServer,
			IpAddressRange: &official.IPAddressRange{Start: d.RangeStart.ValueString(), Stop: d.RangeStop.ValueString()},
		}
		if !d.DomainName.IsNull() && !d.DomainName.IsUnknown() {
			v := d.DomainName.ValueString()
			server.DomainName = &v
		}
		if !d.LeaseTimeSeconds.IsNull() && !d.LeaseTimeSeconds.IsUnknown() {
			v := safeInt32(d.LeaseTimeSeconds.ValueInt64())
			server.LeaseTimeSeconds = &v
		}
		if !d.DNSServers.IsNull() && !d.DNSServers.IsUnknown() {
			var dns []string
			diags.Append(d.DNSServers.ElementsAs(ctx, &dns, false)...)
			if diags.HasError() {
				return body, diags
			}
			server.DnsServerIpAddressesOverride = &dns
		}
		var dhcpCfg official.GatewayManagedIPv4DHCPConfiguration
		if err := dhcpCfg.FromGatewayManagedIPv4DHCPServerConfiguration(server); err != nil {
			diags.AddError("Failed to build DHCP config", err.Error())
			return body, diags
		}
		ipv4.DhcpConfiguration = &dhcpCfg
	}

	gw := official.GatewayManagedNetworkCreateUpdate{
		Name:              m.Name.ValueString(),
		VlanId:            safeInt32(m.VlanID.ValueInt64()),
		Enabled:           m.Enabled.ValueBool(),
		Ipv4Configuration: ipv4,
	}
	if err := body.FromGatewayManagedNetworkCreateUpdate(gw); err != nil {
		diags.AddError("Failed to build gateway network", err.Error())
		return body, diags
	}
	// FromGatewayManagedNetworkCreateUpdate set Management = GATEWAY on the
	// union and the outer struct; keep the named fields for the marshal override.
	body.Name = m.Name.ValueString()
	body.VlanId = safeInt32(m.VlanID.ValueInt64())
	body.Enabled = m.Enabled.ValueBool()
	return body, diags
}

func flattenNetwork(ctx context.Context, n *official.NetworkDetails) (networkModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := networkModel{
		ID:         types.StringValue(n.Id.String()),
		Name:       types.StringValue(n.Name),
		VlanID:     types.Int64Value(int64(n.VlanId)),
		Enabled:    types.BoolValue(n.Enabled),
		Management: types.StringValue(n.Management),
	}
	if n.Management != mgmtGateway {
		return m, diags
	}
	gmd, err := n.AsGatewayManagedNetworkDetails()
	if err != nil || gmd.Ipv4Configuration == nil {
		// Managed network with no readable L3 body: leave gateway null rather
		// than fabricate one. Surface nothing; the base fields are still valid.
		return m, diags
	}
	ip := gmd.Ipv4Configuration
	gw := &gatewayModel{
		HostIPAddress:    types.StringValue(ip.HostIpAddress),
		PrefixLength:     types.Int64Value(int64(ip.PrefixLength)),
		AutoScaleEnabled: types.BoolValue(ip.AutoScaleEnabled),
	}
	if ip.DhcpConfiguration != nil {
		if server, err := ip.DhcpConfiguration.AsGatewayManagedIPv4DHCPServerConfiguration(); err == nil && server.Mode == dhcpModeServer {
			d := &dhcpModel{
				DNSServers: types.ListNull(types.StringType),
			}
			if server.IpAddressRange != nil {
				d.RangeStart = types.StringValue(server.IpAddressRange.Start)
				d.RangeStop = types.StringValue(server.IpAddressRange.Stop)
			}
			if server.DomainName != nil {
				d.DomainName = types.StringValue(*server.DomainName)
			}
			if server.LeaseTimeSeconds != nil {
				d.LeaseTimeSeconds = types.Int64Value(int64(*server.LeaseTimeSeconds))
			}
			if server.DnsServerIpAddressesOverride != nil {
				list, ld := types.ListValueFrom(ctx, types.StringType, *server.DnsServerIpAddressesOverride)
				diags.Append(ld...)
				d.DNSServers = list
			}
			gw.DHCP = d
		}
	}
	m.Gateway = gw
	return m, diags
}
