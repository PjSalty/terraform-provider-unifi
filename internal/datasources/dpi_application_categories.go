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
	_ datasource.DataSource              = &dpiApplicationCategoriesDataSource{}
	_ datasource.DataSourceWithConfigure = &dpiApplicationCategoriesDataSource{}
)

// NewDpiApplicationCategoriesDataSource returns the unifi_dpi_application_categories
// data source, which lists every DPI application category the controller knows
// about so configurations can resolve a category id for DPI grouping rules.
func NewDpiApplicationCategoriesDataSource() datasource.DataSource {
	return &dpiApplicationCategoriesDataSource{}
}

type dpiApplicationCategoriesDataSource struct {
	data *providerdata.Data
}

type dpiApplicationCategoriesModel struct {
	DpiApplicationCategories []dpiApplicationCategoryModel `tfsdk:"dpi_application_categories"`
}

type dpiApplicationCategoryModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (d *dpiApplicationCategoriesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dpi_application_categories"
}

func (d *dpiApplicationCategoriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All DPI application categories known to the UniFi controller. Use it to resolve a category id for deep packet inspection grouping.",
		Attributes: map[string]schema.Attribute{
			"dpi_application_categories": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The DPI application categories.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true, Description: "DPI application category id."},
						"name": schema.StringAttribute{Computed: true, Description: "DPI application category name."},
					},
				},
			},
		},
	}
}

func (d *dpiApplicationCategoriesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *dpiApplicationCategoriesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	categories, err := official.Collect(d.data.Client.Official().Supporting().ListDpiApplicationCategoriesAll(ctx, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list DPI application categories", err.Error())
		return
	}
	var state dpiApplicationCategoriesModel
	for _, cat := range categories {
		state.DpiApplicationCategories = append(state.DpiApplicationCategories, dpiApplicationCategoryModel{
			ID:   types.Int64Value(int64(cat.Id)),
			Name: types.StringValue(cat.Name),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
