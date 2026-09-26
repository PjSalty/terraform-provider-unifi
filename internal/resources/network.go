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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
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
	mgmtUnmanaged          = "UNMANAGED"
	mgmtGateway            = "GATEWAY"
	dhcpModeServer         = "SERVER"
	defaultIsolation       = false
	defaultCellularBackup  = false
	defaultInternetAccess  = true
	defaultMDNSForwarding  = false
	defaultConflictCheck   = true
	defaultDHCPLeaseSecond = int64(86400)
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
	HostIPAddress         types.String `tfsdk:"host_ip_address"`
	PrefixLength          types.Int64  `tfsdk:"prefix_length"`
	AutoScaleEnabled      types.Bool   `tfsdk:"auto_scale_enabled"`
	IsolationEnabled      types.Bool   `tfsdk:"isolation_enabled"`
	CellularBackupEnabled types.Bool   `tfsdk:"cellular_backup_enabled"`
	InternetAccessEnabled types.Bool   `tfsdk:"internet_access_enabled"`
	MDNSForwardingEnabled types.Bool   `tfsdk:"mdns_forwarding_enabled"`
	ZoneID                types.String `tfsdk:"zone_id"`
	DHCP                  *dhcpModel   `tfsdk:"dhcp"`
}

// dhcpModel is a DHCP server on the gateway-managed subnet. Present block =>
// DHCP server on; omit the block for no DHCP. Mode is always SERVER (RELAY is a
// separate union variant, deferred).
type dhcpModel struct {
	RangeStart                   types.String `tfsdk:"range_start"`
	RangeStop                    types.String `tfsdk:"range_stop"`
	DNSServers                   types.List   `tfsdk:"dns_servers"`
	DomainName                   types.String `tfsdk:"domain_name"`
	LeaseTimeSeconds             types.Int64  `tfsdk:"lease_time_seconds"`
	PingConflictDetectionEnabled types.Bool   `tfsdk:"ping_conflict_detection_enabled"`
}

func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A UniFi network: either VLAN-only (management = UNMANAGED) or gateway-managed " +
			"(management = GATEWAY, routed by the UniFi gateway with an L3 subnet and optional DHCP).",
		MarkdownDescription: "A UniFi network: either VLAN-only (management = UNMANAGED) or gateway-managed " +
			"(management = GATEWAY, routed by the UniFi gateway with an L3 subnet and optional DHCP).\n\n" +
			"Gateway behavior and DHCP lease defaults are provider defaults applied during\n" +
			"planning, including after import. To preserve non-default controller settings,\n" +
			"configure them explicitly; omitting them plans a visible change to the defaults.\n" +
			"An omitted zone preserves a known zone or reads the existing gateway's zone\n" +
			"before update. New gateway networks discover the system-defined Internal zone.\n" +
			"Set `zone_id` explicitly if that observed name cannot be resolved uniquely.\n\n" +
			"The bundled official contracts require gateway isolation, cellular backup, and\n" +
			"internet access since Network 10.1.78, and DHCP lease and ping-conflict detection\n" +
			"throughout the supported versions. mDNS forwarding was required before 10.3.58;\n" +
			"the provider always sends a boolean for compatibility with those releases.",
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
					"isolation_enabled": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Default:  booldefault.StaticBool(defaultIsolation),
						Description: "Whether this network is isolated from other networks. The Integration API requires " +
							"a value on every gateway-network write; omitted configurations default to false.",
					},
					"cellular_backup_enabled": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Default:  booldefault.StaticBool(defaultCellularBackup),
						Description: "Whether this network may use cellular backup when WAN connections are down. The " +
							"Integration API requires a value on every gateway-network write; omitted configurations default to false.",
					},
					"internet_access_enabled": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Default:  booldefault.StaticBool(defaultInternetAccess),
						Description: "Whether devices on this network may access the internet. The Integration API requires " +
							"a value on every gateway-network write; omitted configurations default to true.",
					},
					"mdns_forwarding_enabled": schema.BoolAttribute{
						Optional: true,
						Computed: true,
						Default:  booldefault.StaticBool(defaultMDNSForwarding),
						Description: "Whether this network participates in mDNS forwarding. Omitted configurations default " +
							"to false; a value is always sent for compatibility with Network versions where it is required. " +
							"On Network >= 10.3.58 this explicitly disables forwarding rather than inheriting the site mDNS setting.",
					},
					"zone_id": schema.StringAttribute{
						Optional: true,
						Computed: true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseNonNullStateForUnknown(),
						},
						Description: "Firewall zone UUID associated with this network. When omitted, preserves a known zone " +
							"or reads the existing gateway network's zone before updating. New gateway networks resolve the " +
							"controller's system-defined Internal zone. Set explicitly if automatic discovery is unavailable.",
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
								Description: "Non-empty domain name handed to clients. Omit to clear the domain; an empty controller value reads as null.",
								Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
							},
							"lease_time_seconds": schema.Int64Attribute{
								Optional: true,
								Computed: true,
								Default:  int64default.StaticInt64(defaultDHCPLeaseSecond),
								Description: "DHCP lease time in seconds (0-31536000). The Integration API requires a value " +
									"for DHCP servers; omitted configurations default to 86400 (24 hours).",
								Validators: []validator.Int64{int64validator.Between(0, 31536000)},
							},
							"ping_conflict_detection_enabled": schema.BoolAttribute{
								Optional: true,
								Computed: true,
								Default:  booldefault.StaticBool(defaultConflictCheck),
								Description: "Check whether an address is already in use before the DHCP server offers it. The " +
									"Integration API requires a value; omitted configurations default to true.",
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
	r.resolveGatewayZoneID(ctx, &plan, &resp.Diagnostics)
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
	var prior networkModel
	if plan.Management.ValueString() == mgmtGateway && plan.Gateway != nil &&
		(plan.Gateway.ZoneID.IsNull() || plan.Gateway.ZoneID.IsUnknown()) {
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if plan.Management.ValueString() == mgmtGateway && plan.Gateway != nil &&
		(plan.Gateway.ZoneID.IsNull() || plan.Gateway.ZoneID.IsUnknown()) &&
		prior.Management.ValueString() == mgmtGateway {
		current, err := r.data.Client.Official().Networks().Get(ctx, r.data.SiteID, id)
		if err != nil {
			resp.Diagnostics.AddError("Failed to read existing gateway network zone", err.Error())
			return
		}
		gateway, err := current.AsGatewayManagedNetworkDetails()
		if err != nil || gateway.ZoneId == nil || *gateway.ZoneId == uuid.Nil {
			resp.Diagnostics.AddAttributeError(path.Root("gateway").AtName("zone_id"),
				"Unable to preserve existing gateway network zone",
				"The controller did not return a usable zone ID. Set gateway.zone_id explicitly before updating this network.")
			return
		}
		plan.Gateway.ZoneID = types.StringValue(gateway.ZoneId.String())
	}
	r.resolveGatewayZoneID(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
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

// resolveGatewayZoneID supplies a zone for create or conversion to GATEWAY.
// Observed on Network 10.6.101: HTTP 400, code
// "api.network.validation.missing-zone-id", message "zoneId must not be null", although
// the OpenAPI schema does not mark it required. Update first recovers a missing
// existing gateway zone with GET so discovery cannot move it to another zone.
// "Internal" is an observed system-zone name, not a schema-enumerated identity:
// require SYSTEM_DEFINED origin and a unique match, otherwise fail with an
// explicit zone_id escape hatch rather than guess.
func (r *networkResource) resolveGatewayZoneID(ctx context.Context, m *networkModel, diags *diag.Diagnostics) {
	if m.Management.ValueString() != mgmtGateway || m.Gateway == nil ||
		(!m.Gateway.ZoneID.IsNull() && !m.Gateway.ZoneID.IsUnknown()) {
		return
	}
	var found *uuid.UUID
	for zone, err := range r.data.Client.Official().Firewall().ListZonesAll(ctx, r.data.SiteID, "") {
		if err != nil {
			diags.AddError("Failed to resolve gateway network zone", err.Error())
			return
		}
		if zone.Name == "Internal" && zone.Metadata.Origin == "SYSTEM_DEFINED" {
			if found != nil || zone.Id == uuid.Nil {
				diags.AddError("Ambiguous gateway network zone", "Set gateway.zone_id explicitly; discovery did not return a unique usable system-defined Internal zone.")
				return
			}
			id := zone.Id
			found = &id
		}
	}
	if found != nil {
		m.Gateway.ZoneID = types.StringValue(found.String())
		return
	}
	diags.AddAttributeError(path.Root("gateway").AtName("zone_id"), "Unable to resolve gateway network zone",
		"The controller requires zone_id for gateway networks, but its system-defined Internal zone was not found. Set gateway.zone_id explicitly.")
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
		leaseTimeSeconds := defaultDHCPLeaseSecond
		if !d.LeaseTimeSeconds.IsNull() && !d.LeaseTimeSeconds.IsUnknown() {
			leaseTimeSeconds = d.LeaseTimeSeconds.ValueInt64()
		}
		pingConflictDetectionEnabled := boolValueOrDefault(d.PingConflictDetectionEnabled, defaultConflictCheck)
		server := official.GatewayManagedIPv4DHCPServerConfiguration{
			Mode:                         dhcpModeServer,
			IpAddressRange:               &official.IPAddressRange{Start: d.RangeStart.ValueString(), Stop: d.RangeStop.ValueString()},
			LeaseTimeSeconds:             int32Pointer(safeInt32(leaseTimeSeconds)),
			PingConflictDetectionEnabled: &pingConflictDetectionEnabled,
		}
		if !d.DomainName.IsNull() && !d.DomainName.IsUnknown() {
			v := d.DomainName.ValueString()
			server.DomainName = &v
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

	isolationEnabled := boolValueOrDefault(m.Gateway.IsolationEnabled, defaultIsolation)
	cellularBackupEnabled := boolValueOrDefault(m.Gateway.CellularBackupEnabled, defaultCellularBackup)
	internetAccessEnabled := boolValueOrDefault(m.Gateway.InternetAccessEnabled, defaultInternetAccess)
	mdnsForwardingEnabled := boolValueOrDefault(m.Gateway.MDNSForwardingEnabled, defaultMDNSForwarding)
	gw := official.GatewayManagedNetworkCreateUpdate{
		Name:                  m.Name.ValueString(),
		VlanId:                safeInt32(m.VlanID.ValueInt64()),
		Enabled:               m.Enabled.ValueBool(),
		IsolationEnabled:      &isolationEnabled,
		CellularBackupEnabled: &cellularBackupEnabled,
		InternetAccessEnabled: &internetAccessEnabled,
		MdnsForwardingEnabled: &mdnsForwardingEnabled,
		Ipv4Configuration:     ipv4,
	}
	if !m.Gateway.ZoneID.IsNull() && !m.Gateway.ZoneID.IsUnknown() {
		zoneID, err := uuid.Parse(m.Gateway.ZoneID.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("gateway").AtName("zone_id"), "Invalid gateway network zone_id", err.Error())
			return body, diags
		}
		gw.ZoneId = &zoneID
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
		HostIPAddress:         types.StringValue(ip.HostIpAddress),
		PrefixLength:          types.Int64Value(int64(ip.PrefixLength)),
		AutoScaleEnabled:      types.BoolValue(ip.AutoScaleEnabled),
		IsolationEnabled:      types.BoolValue(boolPointerOrDefault(gmd.IsolationEnabled, defaultIsolation)),
		CellularBackupEnabled: types.BoolValue(boolPointerOrDefault(gmd.CellularBackupEnabled, defaultCellularBackup)),
		InternetAccessEnabled: types.BoolValue(boolPointerOrDefault(gmd.InternetAccessEnabled, defaultInternetAccess)),
		MDNSForwardingEnabled: types.BoolValue(boolPointerOrDefault(gmd.MdnsForwardingEnabled, defaultMDNSForwarding)),
		ZoneID:                types.StringNull(),
	}
	if gmd.ZoneId != nil {
		gw.ZoneID = types.StringValue(gmd.ZoneId.String())
	}
	if ip.DhcpConfiguration != nil {
		if server, err := ip.DhcpConfiguration.AsGatewayManagedIPv4DHCPServerConfiguration(); err == nil && server.Mode == dhcpModeServer {
			d := &dhcpModel{
				DNSServers:                   types.ListNull(types.StringType),
				DomainName:                   types.StringNull(),
				LeaseTimeSeconds:             types.Int64Value(defaultDHCPLeaseSecond),
				PingConflictDetectionEnabled: types.BoolValue(defaultConflictCheck),
			}
			if server.IpAddressRange != nil {
				d.RangeStart = types.StringValue(server.IpAddressRange.Start)
				d.RangeStop = types.StringValue(server.IpAddressRange.Stop)
			}
			if server.DomainName != nil && *server.DomainName != "" {
				d.DomainName = types.StringValue(*server.DomainName)
			}
			if server.LeaseTimeSeconds != nil {
				d.LeaseTimeSeconds = types.Int64Value(int64(*server.LeaseTimeSeconds))
			}
			if server.PingConflictDetectionEnabled != nil {
				d.PingConflictDetectionEnabled = types.BoolValue(*server.PingConflictDetectionEnabled)
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

func boolValueOrDefault(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

func boolPointerOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func int32Pointer(value int32) *int32 {
	return &value
}
