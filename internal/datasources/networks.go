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
	_ datasource.DataSource              = &networksDataSource{}
	_ datasource.DataSourceWithConfigure = &networksDataSource{}
)

// NewNetworksDataSource returns the unifi_networks data source, which lists
// every network/VLAN on the site so configurations can reference the
// default/system VLANs the unifi_network resource cannot create.
func NewNetworksDataSource() datasource.DataSource {
	return &networksDataSource{}
}

type networksDataSource struct {
	data *providerdata.Data
}

type networksModel struct {
	Networks []networkModel `tfsdk:"networks"`
}

type networkModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	VlanID     types.Int64  `tfsdk:"vlan_id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	Default    types.Bool   `tfsdk:"default"`
	Management types.String `tfsdk:"management"`
}

func (d *networksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_networks"
}

func (d *networksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All networks/VLANs on the site. Use it to reference the default or system-defined VLANs (which the unifi_network resource cannot create) by name or VLAN ID.",
		Attributes: map[string]schema.Attribute{
			"networks": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The site's networks.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.StringAttribute{Computed: true, Description: "Network UUID."},
						"name":       schema.StringAttribute{Computed: true, Description: "Network name."},
						"vlan_id":    schema.Int64Attribute{Computed: true, Description: "VLAN ID (1 for the default network, >= 2 for additional networks)."},
						"enabled":    schema.BoolAttribute{Computed: true, Description: "Whether the network is enabled."},
						"default":    schema.BoolAttribute{Computed: true, Description: "Whether this is the site's default network."},
						"management": schema.StringAttribute{Computed: true, Description: "Management type of the network."},
					},
				},
			},
		},
	}
}

func (d *networksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *networksDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	networks, err := official.Collect(d.data.Client.Official().Networks().ListAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list networks", err.Error())
		return
	}
	var state networksModel
	for _, net := range networks {
		state.Networks = append(state.Networks, networkModel{
			ID:         types.StringValue(net.Id.String()),
			Name:       types.StringValue(net.Name),
			VlanID:     types.Int64Value(int64(net.VlanId)),
			Enabled:    types.BoolValue(net.Enabled),
			Default:    types.BoolValue(net.Default),
			Management: types.StringValue(net.Management),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
