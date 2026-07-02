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
	_ datasource.DataSource              = &wansDataSource{}
	_ datasource.DataSourceWithConfigure = &wansDataSource{}
)

// NewWansDataSource returns the unifi_wans data source, which lists every WAN
// configured on the site so configurations can resolve WAN UUIDs.
func NewWansDataSource() datasource.DataSource {
	return &wansDataSource{}
}

type wansDataSource struct {
	data *providerdata.Data
}

type wansModel struct {
	Wans []wanModel `tfsdk:"wans"`
}

type wanModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *wansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wans"
}

func (d *wansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All WANs configured on the site. Use it to resolve a WAN's UUID for other resources.",
		Attributes: map[string]schema.Attribute{
			"wans": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The configured WANs.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{Computed: true, Description: "WAN UUID."},
						"name": schema.StringAttribute{Computed: true, Description: "WAN name."},
					},
				},
			},
		},
	}
}

func (d *wansDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *wansDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	wans, err := official.Collect(d.data.Client.Official().Supporting().ListWansAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list wans", err.Error())
		return
	}
	var state wansModel
	for _, wan := range wans {
		state.Wans = append(state.Wans, wanModel{
			ID:   types.StringValue(wan.Id.String()),
			Name: types.StringValue(wan.Name),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
