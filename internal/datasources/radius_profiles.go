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
	_ datasource.DataSource              = &radiusProfilesDataSource{}
	_ datasource.DataSourceWithConfigure = &radiusProfilesDataSource{}
)

// NewRadiusProfilesDataSource returns the unifi_radius_profiles data source,
// which lists RADIUS profiles so a WPA-Enterprise SSID can resolve a profile's
// UUID by name.
func NewRadiusProfilesDataSource() datasource.DataSource {
	return &radiusProfilesDataSource{}
}

type radiusProfilesDataSource struct {
	data *providerdata.Data
}

type radiusProfilesModel struct {
	RadiusProfiles []radiusProfileModel `tfsdk:"radius_profiles"`
}

type radiusProfileModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *radiusProfilesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_radius_profiles"
}

func (d *radiusProfilesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All RADIUS profiles on the site. Use it to resolve a RADIUS profile's UUID by name to attach to a WPA-Enterprise SSID.",
		Attributes: map[string]schema.Attribute{
			"radius_profiles": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The RADIUS profiles.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{Computed: true, Description: "RADIUS profile UUID."},
						"name": schema.StringAttribute{Computed: true, Description: "RADIUS profile name."},
					},
				},
			},
		},
	}
}

func (d *radiusProfilesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *radiusProfilesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	profiles, err := official.Collect(d.data.Client.Official().Supporting().ListRadiusProfilesAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list RADIUS profiles", err.Error())
		return
	}
	var state radiusProfilesModel
	for _, p := range profiles {
		state.RadiusProfiles = append(state.RadiusProfiles, radiusProfileModel{
			ID:   types.StringValue(p.Id.String()),
			Name: types.StringValue(p.Name),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
