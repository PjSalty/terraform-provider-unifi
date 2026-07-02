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
	_ datasource.DataSource              = &dpiApplicationsDataSource{}
	_ datasource.DataSourceWithConfigure = &dpiApplicationsDataSource{}
)

// NewDPIApplicationsDataSource returns the unifi_dpi_applications data source,
// which lists every deep-packet-inspection application the controller knows so
// configurations can resolve an application id for DPI-based rules.
func NewDPIApplicationsDataSource() datasource.DataSource {
	return &dpiApplicationsDataSource{}
}

type dpiApplicationsDataSource struct {
	data *providerdata.Data
}

type dpiApplicationsModel struct {
	DPIApplications []dpiApplicationModel `tfsdk:"dpi_applications"`
}

type dpiApplicationModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *dpiApplicationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dpi_applications"
}

func (d *dpiApplicationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All deep-packet-inspection (DPI) applications the controller recognizes. Use it to resolve a DPI application id for DPI-based rules.",
		Attributes: map[string]schema.Attribute{
			"dpi_applications": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The recognized DPI applications.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true, Description: "DPI application id."},
						"name": schema.StringAttribute{Computed: true, Description: "DPI application name."},
					},
				},
			},
		},
	}
}

func (d *dpiApplicationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *dpiApplicationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	apps, err := official.Collect(d.data.Client.Official().Supporting().ListDpiApplicationsAll(ctx, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list DPI applications", err.Error())
		return
	}
	var state dpiApplicationsModel
	for _, app := range apps {
		state.DPIApplications = append(state.DPIApplications, dpiApplicationModel{
			ID:   types.Int64Value(int64(app.Id)),
			Name: types.StringValue(app.Name),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
