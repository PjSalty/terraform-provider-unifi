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
	_ datasource.DataSource              = &sitesDataSource{}
	_ datasource.DataSourceWithConfigure = &sitesDataSource{}
)

// NewSitesDataSource returns the unifi_sites data source, which lists every
// local site so configurations can resolve a site's UUID and legacy name.
func NewSitesDataSource() datasource.DataSource {
	return &sitesDataSource{}
}

type sitesDataSource struct {
	data *providerdata.Data
}

type sitesModel struct {
	Sites []siteModel `tfsdk:"sites"`
}

type siteModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	InternalReference types.String `tfsdk:"internal_reference"`
}

func (d *sitesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sites"
}

func (d *sitesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All local UniFi sites. Use it to resolve a site's UUID or its legacy internal reference name.",
		Attributes: map[string]schema.Attribute{
			"sites": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The local sites.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                 schema.StringAttribute{Computed: true, Description: "Official-API site UUID."},
						"name":               schema.StringAttribute{Computed: true, Description: "Site display name."},
						"internal_reference": schema.StringAttribute{Computed: true, Description: "Legacy site name used by the Internal API."},
					},
				},
			},
		},
	}
}

func (d *sitesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *sitesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	sites, err := official.Collect(d.data.Client.Official().Sites().ListAll(ctx, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list sites", err.Error())
		return
	}
	var state sitesModel
	for _, site := range sites {
		state.Sites = append(state.Sites, siteModel{
			ID:                types.StringValue(site.ID.String()),
			Name:              types.StringValue(site.Name),
			InternalReference: types.StringValue(site.InternalReference),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
