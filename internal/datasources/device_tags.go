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
	_ datasource.DataSource              = &deviceTagsDataSource{}
	_ datasource.DataSourceWithConfigure = &deviceTagsDataSource{}
)

// NewDeviceTagsDataSource returns the unifi_device_tags data source, which lists
// every device tag on the site so configurations can resolve a tag's UUID and
// the set of devices it groups.
func NewDeviceTagsDataSource() datasource.DataSource {
	return &deviceTagsDataSource{}
}

type deviceTagsDataSource struct {
	data *providerdata.Data
}

type deviceTagsModel struct {
	DeviceTags []deviceTagModel `tfsdk:"device_tags"`
}

type deviceTagModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	DeviceIDs types.List   `tfsdk:"device_ids"`
}

func (d *deviceTagsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_tags"
}

func (d *deviceTagsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All device tags on the site. Use it to resolve a tag's UUID and the devices it groups.",
		Attributes: map[string]schema.Attribute{
			"device_tags": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The device tags.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{Computed: true, Description: "Device tag UUID."},
						"name": schema.StringAttribute{Computed: true, Description: "Device tag name."},
						"device_ids": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "UUIDs of the devices this tag is applied to.",
						},
					},
				},
			},
		},
	}
}

func (d *deviceTagsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *deviceTagsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tags, err := official.Collect(d.data.Client.Official().Supporting().ListDeviceTagsAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list device tags", err.Error())
		return
	}
	var state deviceTagsModel
	for _, tag := range tags {
		deviceIDs := make([]string, 0, len(tag.DeviceIds))
		for _, id := range tag.DeviceIds {
			deviceIDs = append(deviceIDs, id.String())
		}
		list, diags := types.ListValueFrom(ctx, types.StringType, deviceIDs)
		resp.Diagnostics.Append(diags...)
		state.DeviceTags = append(state.DeviceTags, deviceTagModel{
			ID:        types.StringValue(tag.Id.String()),
			Name:      types.StringValue(tag.Name),
			DeviceIDs: list,
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
