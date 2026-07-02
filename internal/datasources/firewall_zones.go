package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi/official"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

var (
	_ datasource.DataSource              = &firewallZonesDataSource{}
	_ datasource.DataSourceWithConfigure = &firewallZonesDataSource{}
)

// NewFirewallZonesDataSource returns the unifi_firewall_zones data source, which
// lists every firewall zone on the site. System-defined zones (origin SYSTEM)
// can never be created by a resource, yet firewall policies must reference their
// UUIDs, so this data source is how a configuration resolves them.
func NewFirewallZonesDataSource() datasource.DataSource {
	return &firewallZonesDataSource{}
}

type firewallZonesDataSource struct {
	data *providerdata.Data
}

type firewallZonesModel struct {
	FirewallZones []firewallZoneModel `tfsdk:"firewall_zones"`
}

type firewallZoneModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	NetworkIDs types.List   `tfsdk:"network_ids"`
	Origin     types.String `tfsdk:"origin"`
}

func (d *firewallZonesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_zones"
}

func (d *firewallZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All firewall zones on the site. System-defined zones cannot be created by a resource but must be referenced by firewall policies; use this to resolve their UUIDs.",
		Attributes: map[string]schema.Attribute{
			"firewall_zones": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The firewall zones.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{Computed: true, Description: "Firewall zone UUID."},
						"name": schema.StringAttribute{Computed: true, Description: "Firewall zone name."},
						"network_ids": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "UUIDs of the networks assigned to this zone.",
						},
						"origin": schema.StringAttribute{Computed: true, Description: "Whether the zone is SYSTEM- or USER-defined."},
					},
				},
			},
		},
	}
}

func (d *firewallZonesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *providerdata.Data, got %T. This is a provider bug.", req.ProviderData))
		return
	}
	d.data = pd
}

func (d *firewallZonesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	zones, err := official.Collect(d.data.Client.Official().Firewall().ListZonesAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list firewall zones", err.Error())
		return
	}
	var state firewallZonesModel
	for _, z := range zones {
		ids := make([]string, 0, len(z.NetworkIds))
		for _, id := range z.NetworkIds {
			ids = append(ids, id.String())
		}
		networkIDs, diags := types.ListValueFrom(ctx, types.StringType, ids)
		resp.Diagnostics.Append(diags...)
		state.FirewallZones = append(state.FirewallZones, firewallZoneModel{
			ID:         types.StringValue(z.Id.String()),
			Name:       types.StringValue(z.Name),
			NetworkIDs: networkIDs,
			Origin:     types.StringValue(z.Metadata.Origin),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
