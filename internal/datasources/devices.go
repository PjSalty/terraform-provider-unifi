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
	_ datasource.DataSource              = &devicesDataSource{}
	_ datasource.DataSourceWithConfigure = &devicesDataSource{}
)

// NewDevicesDataSource returns the unifi_devices data source, which lists every
// adopted device so configurations can resolve AP UUIDs for SSID targeting.
func NewDevicesDataSource() datasource.DataSource {
	return &devicesDataSource{}
}

type devicesDataSource struct {
	data *providerdata.Data
}

type devicesModel struct {
	Devices []deviceModel `tfsdk:"devices"`
}

type deviceModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	MacAddress types.String `tfsdk:"mac_address"`
	Model      types.String `tfsdk:"model"`
	IPAddress  types.String `tfsdk:"ip_address"`
	State      types.String `tfsdk:"state"`
}

func (d *devicesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *devicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All adopted UniFi devices on the site. Use it to resolve an AP's UUID for a unifi_wifi_broadcast broadcasting_device_filter.",
		Attributes: map[string]schema.Attribute{
			"devices": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The adopted devices.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, Description: "Device UUID."},
						"name":        schema.StringAttribute{Computed: true, Description: "Device name."},
						"mac_address": schema.StringAttribute{Computed: true, Description: "MAC address."},
						"model":       schema.StringAttribute{Computed: true, Description: "Hardware model."},
						"ip_address":  schema.StringAttribute{Computed: true, Description: "Current IP address."},
						"state":       schema.StringAttribute{Computed: true, Description: "Adoption/connection state (e.g. ONLINE, OFFLINE)."},
					},
				},
			},
		},
	}
}

func (d *devicesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *devicesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	devices, err := official.Collect(d.data.Client.Official().Devices().ListAdoptedAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list devices", err.Error())
		return
	}
	var state devicesModel
	for _, dev := range devices {
		state.Devices = append(state.Devices, deviceModel{
			ID:         types.StringValue(dev.Id.String()),
			Name:       types.StringValue(dev.Name),
			MacAddress: types.StringValue(dev.MacAddress),
			Model:      types.StringValue(dev.Model),
			IPAddress:  types.StringValue(dev.IpAddress),
			State:      types.StringValue(string(dev.State)),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
