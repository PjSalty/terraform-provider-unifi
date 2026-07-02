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
	_ resource.Resource                = &firewallPolicyResource{}
	_ resource.ResourceWithConfigure   = &firewallPolicyResource{}
	_ resource.ResourceWithImportState = &firewallPolicyResource{}
)

// firewallPolicyProtocolNames is the named-protocol enum accepted by the
// ip_protocol_scope.protocol field. It is the union of the IPv4 and IPv6 named
// protocols (so it covers icmp and icmpv6); the controller validates that the
// chosen protocol is legal for the scope's ip_version.
var firewallPolicyProtocolNames = []string{
	"ah", "ax.25", "dccp", "ddp", "egp", "eigrp", "encap", "esp", "etherip",
	"fc", "ggp", "gre", "hip", "hmp", "icmp", "icmpv6", "idpr-cmtp", "idrp",
	"igmp", "igp", "ip", "ipcomp", "ipencap", "ipip", "ipv6", "ipv6-frag",
	"ipv6-nonxt", "ipv6-opts", "ipv6-route", "isis", "iso-tp4", "l2tp", "manet",
	"mobility-header", "mpls-in-ip", "ospf", "pim", "pup", "rdp", "rohc", "rspf",
	"rsvp", "sctp", "shim6", "skip", "st", "tcp", "tcp_udp", "udp", "udplite",
	"vmtp", "vrrp", "wesp", "xns-idp", "xtp",
}

// NewFirewallPolicyResource returns the unifi_firewall_policy resource: a
// zone-based firewall policy governing traffic between two firewall zones.
func NewFirewallPolicyResource() resource.Resource {
	return &firewallPolicyResource{}
}

type firewallPolicyResource struct {
	data *providerdata.Data
}

type firewallPolicyModel struct {
	ID                    types.String                    `tfsdk:"id"`
	Name                  types.String                    `tfsdk:"name"`
	Enabled               types.Bool                      `tfsdk:"enabled"`
	LoggingEnabled        types.Bool                      `tfsdk:"logging_enabled"`
	Description           types.String                    `tfsdk:"description"`
	Index                 types.Int64                     `tfsdk:"index"`
	Origin                types.String                    `tfsdk:"origin"`
	ConnectionStateFilter types.List                      `tfsdk:"connection_state_filter"`
	IpsecFilter           types.String                    `tfsdk:"ipsec_filter"`
	Action                *firewallPolicyActionModel      `tfsdk:"action"`
	IPProtocolScope       *firewallPolicyScopeModel       `tfsdk:"ip_protocol_scope"`
	Source                *firewallPolicySourceModel      `tfsdk:"source"`
	Destination           *firewallPolicyDestinationModel `tfsdk:"destination"`
}

type firewallPolicyActionModel struct {
	Type               types.String `tfsdk:"type"`
	AllowReturnTraffic types.Bool   `tfsdk:"allow_return_traffic"`
}

type firewallPolicyScopeModel struct {
	IPVersion types.String `tfsdk:"ip_version"`
	Protocol  types.String `tfsdk:"protocol"`
}

type firewallPolicySourceModel struct {
	ZoneID      types.String `tfsdk:"zone_id"`
	NetworkIDs  types.List   `tfsdk:"network_ids"`
	IPAddresses types.List   `tfsdk:"ip_addresses"`
	Ports       types.List   `tfsdk:"ports"`
}

type firewallPolicyDestinationModel struct {
	ZoneID      types.String `tfsdk:"zone_id"`
	NetworkIDs  types.List   `tfsdk:"network_ids"`
	IPAddresses types.List   `tfsdk:"ip_addresses"`
	Ports       types.List   `tfsdk:"ports"`
	Domains     types.List   `tfsdk:"domains"`
}

func (r *firewallPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_policy"
}

func (r *firewallPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A zone-based firewall policy on the UniFi controller. A policy governs " +
			"traffic from a source firewall zone to a destination firewall zone.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Firewall policy UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Firewall policy name.",
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the policy is enabled.",
			},
			"logging_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Generate syslog entries when traffic matches this policy.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Free-form policy description.",
			},
			"index": schema.Int64Attribute{
				Computed:    true,
				Description: "Evaluation order index assigned by the controller.",
			},
			"origin": schema.StringAttribute{
				Computed:    true,
				Description: "Policy origin as reported by the controller (e.g. USER_DEFINED, SYSTEM_DEFINED).",
			},
			"connection_state_filter": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Match on firewall connection state. Any of NEW, INVALID, ESTABLISHED, RELATED. Omit to match all states.",
			},
			"ipsec_filter": schema.StringAttribute{
				Optional:    true,
				Description: "Match IPsec-encrypted traffic: MATCH_ENCRYPTED or MATCH_NOT_ENCRYPTED. Omit to match all traffic.",
				Validators:  []validator.String{stringvalidator.OneOf("MATCH_ENCRYPTED", "MATCH_NOT_ENCRYPTED")},
			},
			"action": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Action applied to matched traffic.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: "Action: ALLOW, BLOCK, or REJECT.",
						Validators:  []validator.String{stringvalidator.OneOf("ALLOW", "BLOCK", "REJECT")},
					},
					"allow_return_traffic": schema.BoolAttribute{
						Optional: true,
						Description: "For ALLOW actions, automatically allow return traffic on the mirrored " +
							"zone pair. Ignored for BLOCK/REJECT.",
					},
				},
			},
			"ip_protocol_scope": schema.SingleNestedAttribute{
				Required:    true,
				Description: "IP version and protocol matching scope.",
				Attributes: map[string]schema.Attribute{
					"ip_version": schema.StringAttribute{
						Required:    true,
						Description: "IP version to match: IPV4, IPV6, or IPV4_AND_IPV6.",
						Validators:  []validator.String{stringvalidator.OneOf("IPV4", "IPV6", "IPV4_AND_IPV6")},
					},
					"protocol": schema.StringAttribute{
						Optional: true,
						Description: "Named protocol to match (e.g. tcp, udp, tcp_udp, icmp, icmpv6). " +
							"Omit to match all protocols.",
						Validators: []validator.String{stringvalidator.OneOf(firewallPolicyProtocolNames...)},
					},
				},
			},
			"source": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Traffic source. Set at most one of network_ids, ip_addresses, or ports.",
				Attributes: map[string]schema.Attribute{
					"zone_id": schema.StringAttribute{
						Required:    true,
						Description: "UUID of the firewall zone the traffic originates from.",
					},
					"network_ids": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "UUIDs of networks to match as the source.",
					},
					"ip_addresses": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "IP addresses to match as the source.",
					},
					"ports": schema.ListAttribute{
						Optional:    true,
						ElementType: types.Int64Type,
						Description: "Source ports to match.",
					},
				},
			},
			"destination": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Traffic destination. Set at most one of network_ids, ip_addresses, ports, or domains.",
				Attributes: map[string]schema.Attribute{
					"zone_id": schema.StringAttribute{
						Required:    true,
						Description: "UUID of the firewall zone the traffic is destined to.",
					},
					"network_ids": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "UUIDs of networks to match as the destination.",
					},
					"ip_addresses": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "IP addresses to match as the destination.",
					},
					"ports": schema.ListAttribute{
						Optional:    true,
						ElementType: types.Int64Type,
						Description: "Destination ports to match.",
					},
					"domains": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "Domains to match as the destination.",
					},
				},
			},
		},
	}
}

func (r *firewallPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *firewallPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan firewallPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandFirewallPolicy(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Firewall().CreatePolicy(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create firewall policy", err.Error())
		return
	}
	state, diags := flattenFirewallPolicy(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall policy id", err.Error())
		return
	}
	got, err := r.data.Client.Official().Firewall().GetPolicy(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read firewall policy", err.Error())
		return
	}
	newState, diags := flattenFirewallPolicy(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *firewallPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan firewallPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall policy id", err.Error())
		return
	}
	body, diags := expandFirewallPolicy(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().Firewall().UpdatePolicy(ctx, r.data.SiteID, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update firewall policy", err.Error())
		return
	}
	newState, diags := flattenFirewallPolicy(ctx, got)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *firewallPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state firewallPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid firewall policy id", err.Error())
		return
	}
	// The controller refuses to delete system-defined policies; that error is
	// surfaced as-is. ErrNotFound means the policy is already gone (success).
	if err := r.data.Client.Official().Firewall().DeletePolicy(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete firewall policy", err.Error())
	}
}

func (r *firewallPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- expand ------------------------------------------------------------------

func expandFirewallPolicy(ctx context.Context, m firewallPolicyModel) (official.FirewallPolicyCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := official.FirewallPolicyCreateOrUpdate{
		Name:           m.Name.ValueString(),
		Enabled:        m.Enabled.ValueBool(),
		LoggingEnabled: m.LoggingEnabled.ValueBool(),
	}
	if !m.Description.IsNull() && m.Description.ValueString() != "" {
		desc := m.Description.ValueString()
		body.Description = &desc
	}

	if m.Action == nil {
		diags.AddError("Missing action", "The action block is required.")
	} else {
		body.Action = expandFirewallPolicyAction(m.Action, &diags)
	}
	if m.IPProtocolScope == nil {
		diags.AddError("Missing ip_protocol_scope", "The ip_protocol_scope block is required.")
	} else {
		body.IpProtocolScope = expandFirewallPolicyScope(m.IPProtocolScope, &diags)
	}
	if m.Source == nil {
		diags.AddError("Missing source", "The source block is required.")
	} else {
		body.Source = expandFirewallPolicySource(ctx, m.Source, &diags)
	}
	if m.Destination == nil {
		diags.AddError("Missing destination", "The destination block is required.")
	} else {
		body.Destination = expandFirewallPolicyDestination(ctx, m.Destination, &diags)
	}

	if states := expandConnectionStates(ctx, m.ConnectionStateFilter, &diags); states != nil {
		body.ConnectionStateFilter = &states
	}
	if !m.IpsecFilter.IsNull() && m.IpsecFilter.ValueString() != "" {
		f := official.FirewallPolicyIpsecFilter(m.IpsecFilter.ValueString())
		body.IpsecFilter = &f
	}
	return body, diags
}

func expandFirewallPolicyAction(m *firewallPolicyActionModel, diags *diag.Diagnostics) official.FirewallPolicyAction {
	var a official.FirewallPolicyAction
	switch m.Type.ValueString() {
	case "ALLOW":
		allow := official.FirewallPolicyActionAllow{Type: "ALLOW"}
		if !m.AllowReturnTraffic.IsNull() && !m.AllowReturnTraffic.IsUnknown() {
			b := m.AllowReturnTraffic.ValueBool()
			allow.AllowReturnTraffic = &b
		}
		if err := a.FromFirewallPolicyActionAllow(allow); err != nil {
			diags.AddError("Failed to encode ALLOW action", err.Error())
		}
	case "BLOCK":
		if err := a.FromFirewallPolicyActionBlock(official.FirewallPolicyActionBlock{Type: "BLOCK"}); err != nil {
			diags.AddError("Failed to encode BLOCK action", err.Error())
		}
	case "REJECT":
		if err := a.FromFirewallPolicyActionReject(official.FirewallPolicyActionReject{Type: "REJECT"}); err != nil {
			diags.AddError("Failed to encode REJECT action", err.Error())
		}
	default:
		diags.AddError("Invalid action type", m.Type.ValueString()+" (want ALLOW, BLOCK, or REJECT)")
	}
	return a
}

// expandFirewallPolicyScope builds the ip_protocol_scope union. Each ip_version
// carries a distinct protocol-filter union type, so the named-protocol variant
// is built per branch. shortcut: only the named_protocol variant is implemented;
// add the protocol_number and protocol_preset variants when a policy needs to
// match by IANA number or a TCP/UDP preset.
func expandFirewallPolicyScope(m *firewallPolicyScopeModel, diags *diag.Diagnostics) official.FirewallPolicyIPProtocolScope {
	var scope official.FirewallPolicyIPProtocolScope
	proto := ""
	if !m.Protocol.IsNull() && !m.Protocol.IsUnknown() {
		proto = m.Protocol.ValueString()
	}

	switch m.IPVersion.ValueString() {
	case "IPV4_AND_IPV6":
		inner := official.FirewallPolicyIpv4AndIpv6ProtocolScope{IpVersion: "IPV4_AND_IPV6"}
		if proto != "" {
			named := official.FirewallPolicyIPv4AndIPv6NamedProtocol{Name: official.FirewallPolicyIPv4AndIPv6NamedProtocolName(proto)}
			nf := official.FirewallPolicyIpv4AndIpv6NamedProtocolFilter{Type: "NAMED_PROTOCOL", Protocol: &named}
			var pf official.FirewallPolicyIPv4AndIPv6Protocol
			if err := pf.FromFirewallPolicyIpv4AndIpv6NamedProtocolFilter(nf); err != nil {
				diags.AddError("Failed to encode protocol filter", err.Error())
			}
			inner.ProtocolFilter = &pf
		}
		if err := scope.FromFirewallPolicyIpv4AndIpv6ProtocolScope(inner); err != nil {
			diags.AddError("Failed to encode ip_protocol_scope", err.Error())
		}
	case "IPV4":
		inner := official.FirewallPolicyIpv4ProtocolScope{IpVersion: "IPV4"}
		if proto != "" {
			name := official.FirewallPolicyIPv4NamedProtocolName(proto)
			named := official.FirewallPolicyIPv4NamedProtocol{Name: &name}
			nf := official.FirewallPolicyIpv4NamedProtocolFilter{Type: "NAMED_PROTOCOL", Protocol: &named}
			var pf official.FirewallPolicyIPv4Protocol
			if err := pf.FromFirewallPolicyIpv4NamedProtocolFilter(nf); err != nil {
				diags.AddError("Failed to encode protocol filter", err.Error())
			}
			inner.ProtocolFilter = &pf
		}
		if err := scope.FromFirewallPolicyIpv4ProtocolScope(inner); err != nil {
			diags.AddError("Failed to encode ip_protocol_scope", err.Error())
		}
	case "IPV6":
		inner := official.FirewallPolicyIpv6ProtocolScope{IpVersion: "IPV6"}
		if proto != "" {
			name := official.FirewallPolicyIPv6NamedProtocolName(proto)
			named := official.FirewallPolicyIPv6NamedProtocol{Name: &name}
			nf := official.FirewallPolicyIpv6NamedProtocolFilter{Type: "NAMED_PROTOCOL", Protocol: &named}
			var pf official.FirewallPolicyIPv6Protocol
			if err := pf.FromFirewallPolicyIpv6NamedProtocolFilter(nf); err != nil {
				diags.AddError("Failed to encode protocol filter", err.Error())
			}
			inner.ProtocolFilter = &pf
		}
		if err := scope.FromFirewallPolicyIpv6ProtocolScope(inner); err != nil {
			diags.AddError("Failed to encode ip_protocol_scope", err.Error())
		}
	default:
		diags.AddError("Invalid ip_version", m.IPVersion.ValueString()+" (want IPV4, IPV6, or IPV4_AND_IPV6)")
	}
	return scope
}

func expandFirewallPolicySource(ctx context.Context, m *firewallPolicySourceModel, diags *diag.Diagnostics) official.FirewallPolicySource {
	var src official.FirewallPolicySource
	if zid, err := uuid.Parse(m.ZoneID.ValueString()); err != nil {
		diags.AddError("Invalid source zone_id", err.Error())
	} else {
		src.ZoneId = zid
	}

	networkIDs := fpListToUUIDs(ctx, m.NetworkIDs, "source network_ids", diags)
	ipAddrs := listToStrings(ctx, m.IPAddresses, diags)
	ports := listToInt32s(ctx, m.Ports, diags)

	if fpFilterCount(len(networkIDs), len(ipAddrs), len(ports)) > 1 {
		diags.AddError("Conflicting source traffic filters",
			"Set at most one of network_ids, ip_addresses, or ports on source.")
		return src
	}

	switch {
	case len(networkIDs) > 0:
		v := official.FirewallPolicySourceNetworkFilter{
			Type:          "NETWORK",
			NetworkFilter: &official.FirewallPolicyNetworkFilter{NetworkIds: networkIDs},
		}
		var tf official.FirewallPolicySourceTrafficFilter
		if err := tf.FromFirewallPolicySourceNetworkFilter(v); err != nil {
			diags.AddError("Failed to encode source network filter", err.Error())
		}
		src.TrafficFilter = &tf
	case len(ipAddrs) > 0:
		v := official.FirewallPolicySourceIpAddressFilter{Type: "IP_ADDRESS", IpAddressFilter: fpBuildIPAddressFilter(ipAddrs, diags)}
		var tf official.FirewallPolicySourceTrafficFilter
		if err := tf.FromFirewallPolicySourceIpAddressFilter(v); err != nil {
			diags.AddError("Failed to encode source ip address filter", err.Error())
		}
		src.TrafficFilter = &tf
	case len(ports) > 0:
		v := official.FirewallPolicySourcePortFilter{Type: "PORT", PortFilter: fpBuildPortFilter(ports, diags)}
		var tf official.FirewallPolicySourceTrafficFilter
		if err := tf.FromFirewallPolicySourcePortFilter(v); err != nil {
			diags.AddError("Failed to encode source port filter", err.Error())
		}
		src.TrafficFilter = &tf
	}
	return src
}

func expandFirewallPolicyDestination(ctx context.Context, m *firewallPolicyDestinationModel, diags *diag.Diagnostics) official.FirewallPolicyDestination {
	var dst official.FirewallPolicyDestination
	if zid, err := uuid.Parse(m.ZoneID.ValueString()); err != nil {
		diags.AddError("Invalid destination zone_id", err.Error())
	} else {
		dst.ZoneId = zid
	}

	networkIDs := fpListToUUIDs(ctx, m.NetworkIDs, "destination network_ids", diags)
	ipAddrs := listToStrings(ctx, m.IPAddresses, diags)
	ports := listToInt32s(ctx, m.Ports, diags)
	domains := listToStrings(ctx, m.Domains, diags)

	if fpFilterCount(len(networkIDs), len(ipAddrs), len(ports), len(domains)) > 1 {
		diags.AddError("Conflicting destination traffic filters",
			"Set at most one of network_ids, ip_addresses, ports, or domains on destination.")
		return dst
	}

	switch {
	case len(networkIDs) > 0:
		v := official.FirewallPolicyDestinationNetworkFilter{
			Type:          "NETWORK",
			NetworkFilter: &official.FirewallPolicyNetworkFilter{NetworkIds: networkIDs},
		}
		var tf official.FirewallPolicyDestinationTrafficFilter
		if err := tf.FromFirewallPolicyDestinationNetworkFilter(v); err != nil {
			diags.AddError("Failed to encode destination network filter", err.Error())
		}
		dst.TrafficFilter = &tf
	case len(ipAddrs) > 0:
		v := official.FirewallPolicyDestinationIpAddressFilter{Type: "IP_ADDRESS", IpAddressFilter: fpBuildIPAddressFilter(ipAddrs, diags)}
		var tf official.FirewallPolicyDestinationTrafficFilter
		if err := tf.FromFirewallPolicyDestinationIpAddressFilter(v); err != nil {
			diags.AddError("Failed to encode destination ip address filter", err.Error())
		}
		dst.TrafficFilter = &tf
	case len(ports) > 0:
		v := official.FirewallPolicyDestinationPortFilter{Type: "PORT", PortFilter: fpBuildPortFilter(ports, diags)}
		var tf official.FirewallPolicyDestinationTrafficFilter
		if err := tf.FromFirewallPolicyDestinationPortFilter(v); err != nil {
			diags.AddError("Failed to encode destination port filter", err.Error())
		}
		dst.TrafficFilter = &tf
	case len(domains) > 0:
		spec := official.FirewallPolicySpecificDomainFilter{Type: "DOMAINS", Domains: &domains}
		var df official.FirewallPolicyDomainFilter
		if err := df.FromFirewallPolicySpecificDomainFilter(spec); err != nil {
			diags.AddError("Failed to encode destination domain filter", err.Error())
		}
		v := official.FirewallPolicyDestinationDomainFilter{Type: "DOMAIN", DomainFilter: &df}
		var tf official.FirewallPolicyDestinationTrafficFilter
		if err := tf.FromFirewallPolicyDestinationDomainFilter(v); err != nil {
			diags.AddError("Failed to encode destination domain filter", err.Error())
		}
		dst.TrafficFilter = &tf
	}
	return dst
}

// fpBuildIPAddressFilter wraps a list of IP addresses into the IP_ADDRESSES
// specific filter. shortcut: only single IP_ADDRESS matchers are built; add the
// IP_ADDRESS_RANGE / SUBNET matchers and the trafficMatchingListId variant when
// a policy needs ranges, CIDRs, or a shared matching list.
func fpBuildIPAddressFilter(ips []string, diags *diag.Diagnostics) *official.FirewallPolicyIPAddressFilter {
	items := make([]official.IPMatching, 0, len(ips))
	for _, ip := range ips {
		v := ip
		var m official.IPMatching
		if err := m.FromFirewallPolicyIpMatchingIpAddress(official.FirewallPolicyIpMatchingIpAddress{Type: "IP_ADDRESS", Value: &v}); err != nil {
			diags.AddError("Failed to encode IP address matcher", err.Error())
			continue
		}
		items = append(items, m)
	}
	spec := official.FirewallPolicySpecificIpAddressFilter{Type: "IP_ADDRESSES", Items: &items}
	var f official.FirewallPolicyIPAddressFilter
	if err := f.FromFirewallPolicySpecificIpAddressFilter(spec); err != nil {
		diags.AddError("Failed to encode IP address filter", err.Error())
	}
	return &f
}

// fpBuildPortFilter wraps a list of ports into the PORTS value filter. shortcut:
// only single PORT_NUMBER matchers are built; add PORT_NUMBER_RANGE and the
// trafficMatchingListId variant when a policy needs ranges or a shared list.
func fpBuildPortFilter(ports []int32, diags *diag.Diagnostics) *official.FirewallPolicyPortFilter {
	items := make([]official.PortMatching, 0, len(ports))
	for _, p := range ports {
		v := p
		var m official.PortMatching
		if err := m.FromNumberPortMatching(official.NumberPortMatching{Type: "PORT_NUMBER", Value: &v}); err != nil {
			diags.AddError("Failed to encode port matcher", err.Error())
			continue
		}
		items = append(items, m)
	}
	pvf := official.FirewallPolicyPortValueFilter{Type: "PORTS", Items: &items}
	var f official.FirewallPolicyPortFilter
	if err := f.FromFirewallPolicyPortValueFilter(pvf); err != nil {
		diags.AddError("Failed to encode port filter", err.Error())
	}
	return &f
}

func expandConnectionStates(ctx context.Context, l types.List, diags *diag.Diagnostics) []official.FirewallPolicyConnectionStateFilter {
	strs := listToStrings(ctx, l, diags)
	if strs == nil {
		return nil
	}
	out := make([]official.FirewallPolicyConnectionStateFilter, 0, len(strs))
	for _, s := range strs {
		switch s {
		case "NEW", "INVALID", "ESTABLISHED", "RELATED":
			out = append(out, official.FirewallPolicyConnectionStateFilter(s))
		default:
			diags.AddError("Invalid connection state", s+" (want NEW, INVALID, ESTABLISHED, or RELATED)")
		}
	}
	return out
}

// --- flatten -----------------------------------------------------------------

func flattenFirewallPolicy(ctx context.Context, p *official.FirewallPolicy) (firewallPolicyModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := firewallPolicyModel{
		ID:             types.StringValue(p.Id.String()),
		Name:           types.StringValue(p.Name),
		Enabled:        types.BoolValue(p.Enabled),
		LoggingEnabled: types.BoolValue(p.LoggingEnabled),
		Index:          types.Int64Value(int64(p.Index)),
		Origin:         types.StringValue(p.Metadata.Origin),
		Description:    types.StringNull(),
		IpsecFilter:    types.StringNull(),
	}
	if p.Description != nil {
		m.Description = types.StringValue(*p.Description)
	}
	if p.IpsecFilter != nil {
		m.IpsecFilter = types.StringValue(string(*p.IpsecFilter))
	}

	m.ConnectionStateFilter = types.ListNull(types.StringType)
	if p.ConnectionStateFilter != nil {
		states := make([]string, 0, len(*p.ConnectionStateFilter))
		for _, s := range *p.ConnectionStateFilter {
			states = append(states, string(s))
		}
		l, d := types.ListValueFrom(ctx, types.StringType, states)
		diags.Append(d...)
		m.ConnectionStateFilter = l
	}

	m.Action = flattenFirewallPolicyAction(p.Action)
	m.IPProtocolScope = flattenFirewallPolicyScope(p.IpProtocolScope)
	m.Source = flattenFirewallPolicySource(ctx, p.Source, &diags)
	m.Destination = flattenFirewallPolicyDestination(ctx, p.Destination, &diags)
	return m, diags
}

func flattenFirewallPolicyAction(a official.FirewallPolicyAction) *firewallPolicyActionModel {
	out := &firewallPolicyActionModel{
		Type:               types.StringValue(a.Type),
		AllowReturnTraffic: types.BoolNull(),
	}
	if a.Type == "ALLOW" {
		if allow, err := a.AsFirewallPolicyActionAllow(); err == nil && allow.AllowReturnTraffic != nil {
			out.AllowReturnTraffic = types.BoolValue(*allow.AllowReturnTraffic)
		}
	}
	return out
}

func flattenFirewallPolicyScope(s official.FirewallPolicyIPProtocolScope) *firewallPolicyScopeModel {
	out := &firewallPolicyScopeModel{
		IPVersion: types.StringValue(s.IpVersion),
		Protocol:  types.StringNull(),
	}
	switch s.IpVersion {
	case "IPV4_AND_IPV6":
		if inner, err := s.AsFirewallPolicyIpv4AndIpv6ProtocolScope(); err == nil && inner.ProtocolFilter != nil {
			if disc, _ := inner.ProtocolFilter.Discriminator(); disc == "NAMED_PROTOCOL" {
				if nf, err := inner.ProtocolFilter.AsFirewallPolicyIpv4AndIpv6NamedProtocolFilter(); err == nil && nf.Protocol != nil {
					out.Protocol = types.StringValue(string(nf.Protocol.Name))
				}
			}
		}
	case "IPV4":
		if inner, err := s.AsFirewallPolicyIpv4ProtocolScope(); err == nil && inner.ProtocolFilter != nil {
			if disc, _ := inner.ProtocolFilter.Discriminator(); disc == "NAMED_PROTOCOL" {
				if nf, err := inner.ProtocolFilter.AsFirewallPolicyIpv4NamedProtocolFilter(); err == nil && nf.Protocol != nil && nf.Protocol.Name != nil {
					out.Protocol = types.StringValue(string(*nf.Protocol.Name))
				}
			}
		}
	case "IPV6":
		if inner, err := s.AsFirewallPolicyIpv6ProtocolScope(); err == nil && inner.ProtocolFilter != nil {
			if disc, _ := inner.ProtocolFilter.Discriminator(); disc == "NAMED_PROTOCOL" {
				if nf, err := inner.ProtocolFilter.AsFirewallPolicyIpv6NamedProtocolFilter(); err == nil && nf.Protocol != nil && nf.Protocol.Name != nil {
					out.Protocol = types.StringValue(string(*nf.Protocol.Name))
				}
			}
		}
	}
	return out
}

func flattenFirewallPolicySource(ctx context.Context, s official.FirewallPolicySource, diags *diag.Diagnostics) *firewallPolicySourceModel {
	out := &firewallPolicySourceModel{
		ZoneID:      types.StringValue(s.ZoneId.String()),
		NetworkIDs:  types.ListNull(types.StringType),
		IPAddresses: types.ListNull(types.StringType),
		Ports:       types.ListNull(types.Int64Type),
	}
	if s.TrafficFilter == nil {
		return out
	}
	disc, err := s.TrafficFilter.Discriminator()
	if err != nil {
		return out
	}
	switch disc {
	case "NETWORK":
		if v, err := s.TrafficFilter.AsFirewallPolicySourceNetworkFilter(); err == nil && v.NetworkFilter != nil {
			out.NetworkIDs = fpUUIDsToList(ctx, v.NetworkFilter.NetworkIds, diags)
		}
	case "IP_ADDRESS":
		if v, err := s.TrafficFilter.AsFirewallPolicySourceIpAddressFilter(); err == nil {
			out.IPAddresses = fpFlattenIPAddresses(ctx, v.IpAddressFilter, diags)
		}
	case "PORT":
		if v, err := s.TrafficFilter.AsFirewallPolicySourcePortFilter(); err == nil {
			out.Ports = fpFlattenPorts(ctx, v.PortFilter, diags)
		}
	}
	return out
}

func flattenFirewallPolicyDestination(ctx context.Context, d official.FirewallPolicyDestination, diags *diag.Diagnostics) *firewallPolicyDestinationModel {
	out := &firewallPolicyDestinationModel{
		ZoneID:      types.StringValue(d.ZoneId.String()),
		NetworkIDs:  types.ListNull(types.StringType),
		IPAddresses: types.ListNull(types.StringType),
		Ports:       types.ListNull(types.Int64Type),
		Domains:     types.ListNull(types.StringType),
	}
	if d.TrafficFilter == nil {
		return out
	}
	disc, err := d.TrafficFilter.Discriminator()
	if err != nil {
		return out
	}
	switch disc {
	case "NETWORK":
		if v, err := d.TrafficFilter.AsFirewallPolicyDestinationNetworkFilter(); err == nil && v.NetworkFilter != nil {
			out.NetworkIDs = fpUUIDsToList(ctx, v.NetworkFilter.NetworkIds, diags)
		}
	case "IP_ADDRESS":
		if v, err := d.TrafficFilter.AsFirewallPolicyDestinationIpAddressFilter(); err == nil {
			out.IPAddresses = fpFlattenIPAddresses(ctx, v.IpAddressFilter, diags)
		}
	case "PORT":
		if v, err := d.TrafficFilter.AsFirewallPolicyDestinationPortFilter(); err == nil {
			out.Ports = fpFlattenPorts(ctx, v.PortFilter, diags)
		}
	case "DOMAIN":
		if v, err := d.TrafficFilter.AsFirewallPolicyDestinationDomainFilter(); err == nil && v.DomainFilter != nil {
			if spec, err := v.DomainFilter.AsFirewallPolicySpecificDomainFilter(); err == nil && spec.Domains != nil {
				l, dd := types.ListValueFrom(ctx, types.StringType, *spec.Domains)
				diags.Append(dd...)
				out.Domains = l
			}
		}
	}
	return out
}

func fpFlattenIPAddresses(ctx context.Context, f *official.FirewallPolicyIPAddressFilter, diags *diag.Diagnostics) types.List {
	null := types.ListNull(types.StringType)
	if f == nil {
		return null
	}
	if disc, _ := f.Discriminator(); disc != "IP_ADDRESSES" {
		return null
	}
	spec, err := f.AsFirewallPolicySpecificIpAddressFilter()
	if err != nil || spec.Items == nil {
		return null
	}
	ips := make([]string, 0, len(*spec.Items))
	for _, item := range *spec.Items {
		if disc, _ := item.Discriminator(); disc != "IP_ADDRESS" {
			continue
		}
		if v, err := item.AsFirewallPolicyIpMatchingIpAddress(); err == nil && v.Value != nil {
			ips = append(ips, *v.Value)
		}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, ips)
	diags.Append(d...)
	return l
}

func fpFlattenPorts(ctx context.Context, f *official.FirewallPolicyPortFilter, diags *diag.Diagnostics) types.List {
	null := types.ListNull(types.Int64Type)
	if f == nil {
		return null
	}
	if disc, _ := f.Discriminator(); disc != "PORTS" {
		return null
	}
	pvf, err := f.AsFirewallPolicyPortValueFilter()
	if err != nil || pvf.Items == nil {
		return null
	}
	ports := make([]int64, 0, len(*pvf.Items))
	for _, item := range *pvf.Items {
		if disc, _ := item.Discriminator(); disc != "PORT_NUMBER" {
			continue
		}
		if v, err := item.AsNumberPortMatching(); err == nil && v.Value != nil {
			ports = append(ports, int64(*v.Value))
		}
	}
	l, d := types.ListValueFrom(ctx, types.Int64Type, ports)
	diags.Append(d...)
	return l
}

// --- helpers -----------------------------------------------------------------

func fpFilterCount(counts ...int) int {
	n := 0
	for _, c := range counts {
		if c > 0 {
			n++
		}
	}
	return n
}

func fpListToUUIDs(ctx context.Context, l types.List, field string, diags *diag.Diagnostics) []uuid.UUID {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var strs []string
	diags.Append(l.ElementsAs(ctx, &strs, false)...)
	out := make([]uuid.UUID, 0, len(strs))
	for _, v := range strs {
		id, err := uuid.Parse(v)
		if err != nil {
			diags.AddError("Invalid UUID in "+field, v)
			continue
		}
		out = append(out, id)
	}
	return out
}

func fpUUIDsToList(ctx context.Context, ids []uuid.UUID, diags *diag.Diagnostics) types.List {
	strs := make([]string, 0, len(ids))
	for _, id := range ids {
		strs = append(strs, id.String())
	}
	l, d := types.ListValueFrom(ctx, types.StringType, strs)
	diags.Append(d...)
	return l
}
