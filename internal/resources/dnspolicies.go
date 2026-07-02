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
	_ resource.Resource                = &dnsPolicyResource{}
	_ resource.ResourceWithConfigure   = &dnsPolicyResource{}
	_ resource.ResourceWithImportState = &dnsPolicyResource{}
)

// dnsPolicyTypes is the set of supported DNS policy discriminators.
var dnsPolicyTypes = []string{
	"A_RECORD", "AAAA_RECORD", "CNAME_RECORD",
	"MX_RECORD", "SRV_RECORD", "TXT_RECORD", "FORWARD_DOMAIN",
}

// NewDNSPolicyResource returns the unifi_dns_policy resource (a per-site DNS
// record or forwarding policy).
func NewDNSPolicyResource() resource.Resource {
	return &dnsPolicyResource{}
}

type dnsPolicyResource struct {
	data *providerdata.Data
}

// dnsPolicyModel is a flattened view of every DNS policy variant. The variant
// is selected by `type`; each variant uses only the subset of fields documented
// in the schema. The expand logic dispatches on `type` to the matching union
// variant, so fields that do not apply to the chosen type are ignored.
type dnsPolicyModel struct {
	ID      types.String `tfsdk:"id"`
	Type    types.String `tfsdk:"type"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Domain  types.String `tfsdk:"domain"`

	// A / AAAA
	IPv4Address types.String `tfsdk:"ipv4_address"`
	IPv6Address types.String `tfsdk:"ipv6_address"`

	// shared TTL (A, AAAA, CNAME)
	TTLSeconds types.Int64 `tfsdk:"ttl_seconds"`

	// CNAME
	TargetDomain types.String `tfsdk:"target_domain"`

	// MX
	MailServerDomain types.String `tfsdk:"mail_server_domain"`

	// MX / SRV
	Priority types.Int64 `tfsdk:"priority"`

	// SRV
	Port         types.Int64  `tfsdk:"port"`
	Protocol     types.String `tfsdk:"protocol"`
	ServerDomain types.String `tfsdk:"server_domain"`
	Service      types.String `tfsdk:"service"`
	Weight       types.Int64  `tfsdk:"weight"`

	// TXT
	Text types.String `tfsdk:"text"`

	// FORWARD_DOMAIN
	IPAddress types.String `tfsdk:"ip_address"`
}

func (r *dnsPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_policy"
}

func (r *dnsPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A site DNS policy on the UniFi controller: a DNS record (A, AAAA, " +
			"CNAME, MX, SRV, TXT) or a per-domain forwarding policy (FORWARD_DOMAIN). " +
			"The `type` selects which of the per-variant attributes apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "DNS policy UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "Policy type: A_RECORD, AAAA_RECORD, CNAME_RECORD, MX_RECORD, " +
					"SRV_RECORD, TXT_RECORD, or FORWARD_DOMAIN.",
				Validators: []validator.String{stringvalidator.OneOf(dnsPolicyTypes...)},
				PlanModifiers: []planmodifier.String{
					// The variant is fixed at creation; changing it requires replacement.
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the policy is enabled.",
			},
			"domain": schema.StringAttribute{
				Optional: true,
				Description: "The domain (record name) the policy applies to. Used by every " +
					"variant.",
				Validators: []validator.String{stringvalidator.LengthAtMost(127)},
			},
			"ipv4_address": schema.StringAttribute{
				Optional:    true,
				Description: "IPv4 address. A_RECORD only.",
			},
			"ipv6_address": schema.StringAttribute{
				Optional:    true,
				Description: "IPv6 address. AAAA_RECORD only.",
			},
			"ttl_seconds": schema.Int64Attribute{
				Optional: true,
				Description: "Time to live in seconds. Used by A_RECORD/AAAA_RECORD " +
					"(0-86400) and CNAME_RECORD (0-604800).",
				Validators: []validator.Int64{int64validator.Between(0, 604800)},
			},
			"target_domain": schema.StringAttribute{
				Optional:    true,
				Description: "Canonical (target) domain. CNAME_RECORD only.",
				Validators:  []validator.String{stringvalidator.LengthAtMost(127)},
			},
			"mail_server_domain": schema.StringAttribute{
				Optional:    true,
				Description: "Mail server domain. MX_RECORD only.",
				Validators:  []validator.String{stringvalidator.LengthAtMost(127)},
			},
			"priority": schema.Int64Attribute{
				Optional: true,
				Description: "Priority; a lower number is preferred. MX_RECORD and " +
					"SRV_RECORD (0-65535).",
				Validators: []validator.Int64{int64validator.Between(0, 65535)},
			},
			"port": schema.Int64Attribute{
				Optional:    true,
				Description: "Service port. SRV_RECORD only (0-65535).",
				Validators:  []validator.Int64{int64validator.Between(0, 65535)},
			},
			"protocol": schema.StringAttribute{
				Optional:    true,
				Description: "Protocol used by the service. SRV_RECORD only.",
			},
			"server_domain": schema.StringAttribute{
				Optional:    true,
				Description: "Domain of the server running the service. SRV_RECORD only.",
				Validators:  []validator.String{stringvalidator.LengthAtMost(127)},
			},
			"service": schema.StringAttribute{
				Optional:    true,
				Description: "Service associated with this record. SRV_RECORD only.",
			},
			"weight": schema.Int64Attribute{
				Optional: true,
				Description: "Weight; a relative value for records of the same priority, " +
					"lower is preferred. SRV_RECORD only (0-65535).",
				Validators: []validator.Int64{int64validator.Between(0, 65535)},
			},
			"text": schema.StringAttribute{
				Optional:    true,
				Description: "Text value. TXT_RECORD only (up to 1024 chars).",
				Validators:  []validator.String{stringvalidator.LengthAtMost(1024)},
			},
			"ip_address": schema.StringAttribute{
				Optional:    true,
				Description: "IP address of the DNS server queries are forwarded to. FORWARD_DOMAIN only.",
			},
		},
	}
}

func (r *dnsPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *dnsPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan dnsPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := expandDNSPolicy(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.Client.Official().DNSPolicies().Create(ctx, r.data.SiteID, body)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create DNS policy", err.Error())
		return
	}
	plan.ID = types.StringValue(got.Id.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnsPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid DNS policy id", err.Error())
		return
	}
	got, err := r.data.Client.Official().DNSPolicies().Get(ctx, r.data.SiteID, id)
	if err != nil {
		if errors.Is(err, unifi.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read DNS policy", err.Error())
		return
	}
	// Refresh the reliably-returned top-level fields. shortcut: the variant-only
	// fields (ipv4_address, target_domain, etc.) live inside the discriminated
	// union and are read via the As<Variant>() navigators; that per-variant
	// round-trip refresh is added alongside the acceptance tests against a live
	// controller, where each navigator can be verified end-to-end.
	state.Type = types.StringValue(got.Type)
	state.Enabled = types.BoolValue(got.Enabled)
	if got.Domain != nil {
		state.Domain = types.StringValue(*got.Domain)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *dnsPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan dnsPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid DNS policy id", err.Error())
		return
	}
	body, diags := expandDNSPolicy(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.data.Client.Official().DNSPolicies().Update(ctx, r.data.SiteID, id, body); err != nil {
		resp.Diagnostics.AddError("Failed to update DNS policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnsPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
		return
	}
	var state dnsPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.Parse(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid DNS policy id", err.Error())
		return
	}
	// ErrNotFound means it is already gone, which is success.
	if err := r.data.Client.Official().DNSPolicies().Delete(ctx, r.data.SiteID, id); err != nil && !errors.Is(err, unifi.ErrNotFound) {
		resp.Diagnostics.AddError("Failed to delete DNS policy", err.Error())
	}
}

func (r *dnsPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandDNSPolicy builds the create/update body for a DNS policy.
//
// Every variant-specific field (ipv4Address, targetDomain, ...) lives ONLY on
// the union variant, so we MUST populate the body through the matching
// From<Variant>CreateUpdate helper (it sets t.union and t.Type). The helper does
// NOT touch the named Enabled field, so we set body.Enabled directly afterward;
// in DNSPolicyCreateOrUpdate.MarshalJSON the named `enabled`/`type` are written
// after (and therefore override) the union, which is exactly what we want.
func expandDNSPolicy(m dnsPolicyModel) (official.DNSPolicyCreateOrUpdate, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body official.DNSPolicyCreateOrUpdate
	var err error

	enabled := m.Enabled.ValueBool()

	switch m.Type.ValueString() {
	case "A_RECORD":
		err = body.FromDnsARecordCreateUpdate(official.DnsARecordCreateUpdate{
			Type:        "A_RECORD",
			Enabled:     enabled,
			Domain:      strPtr(m.Domain),
			Ipv4Address: strPtr(m.IPv4Address),
			TtlSeconds:  int32Ptr(m.TTLSeconds),
		})
	case "AAAA_RECORD":
		err = body.FromDnsAaaaRecordCreateUpdate(official.DnsAaaaRecordCreateUpdate{
			Type:        "AAAA_RECORD",
			Enabled:     enabled,
			Domain:      strPtr(m.Domain),
			Ipv6Address: strPtr(m.IPv6Address),
			TtlSeconds:  int32Ptr(m.TTLSeconds),
		})
	case "CNAME_RECORD":
		err = body.FromDnsCnameRecordCreateUpdate(official.DnsCnameRecordCreateUpdate{
			Type:         "CNAME_RECORD",
			Enabled:      enabled,
			Domain:       strPtr(m.Domain),
			TargetDomain: strPtr(m.TargetDomain),
			TtlSeconds:   int32Ptr(m.TTLSeconds),
		})
	case "MX_RECORD":
		err = body.FromDnsMxRecordCreateUpdate(official.DnsMxRecordCreateUpdate{
			Type:             "MX_RECORD",
			Enabled:          enabled,
			Domain:           strPtr(m.Domain),
			MailServerDomain: strPtr(m.MailServerDomain),
			Priority:         int32Ptr(m.Priority),
		})
	case "SRV_RECORD":
		err = body.FromDnsSrvRecordCreateUpdate(official.DnsSrvRecordCreateUpdate{
			Type:         "SRV_RECORD",
			Enabled:      enabled,
			Domain:       strPtr(m.Domain),
			Port:         int32Ptr(m.Port),
			Priority:     int32Ptr(m.Priority),
			Protocol:     strPtr(m.Protocol),
			ServerDomain: strPtr(m.ServerDomain),
			Service:      strPtr(m.Service),
			Weight:       int32Ptr(m.Weight),
		})
	case "TXT_RECORD":
		err = body.FromDnsTxtRecordCreateUpdate(official.DnsTxtRecordCreateUpdate{
			Type:    "TXT_RECORD",
			Enabled: enabled,
			Domain:  strPtr(m.Domain),
			Text:    strPtr(m.Text),
		})
	case "FORWARD_DOMAIN":
		err = body.FromDnsForwardDomainPolicyCreateUpdate(official.DnsForwardDomainPolicyCreateUpdate{
			Type:      "FORWARD_DOMAIN",
			Enabled:   enabled,
			Domain:    strPtr(m.Domain),
			IpAddress: strPtr(m.IPAddress),
		})
	default:
		diags.AddError("Unsupported DNS policy type", m.Type.ValueString())
		return body, diags
	}
	if err != nil {
		diags.AddError("Failed to encode DNS policy body", err.Error())
		return body, diags
	}

	// The From* helper sets Type but leaves the named Enabled field zero; set it
	// directly so MarshalJSON emits the intended value.
	body.Enabled = enabled
	return body, diags
}

// strPtr returns a *string for a set, non-empty value, else nil so the field is
// omitted (the API treats these as optional).
func strPtr(s types.String) *string {
	if s.IsNull() || s.IsUnknown() || s.ValueString() == "" {
		return nil
	}
	v := s.ValueString()
	return &v
}

// int32Ptr returns a *int32 for a set value, else nil so the field is omitted.
func int32Ptr(i types.Int64) *int32 {
	if i.IsNull() || i.IsUnknown() {
		return nil
	}
	v := safeInt32(i.ValueInt64())
	return &v
}
