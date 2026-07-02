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
	_ datasource.DataSource              = &wifiBroadcastsDataSource{}
	_ datasource.DataSourceWithConfigure = &wifiBroadcastsDataSource{}
)

// NewWifiBroadcastsDataSource returns the unifi_wifi_broadcasts data source,
// which lists every SSID on the site so configurations can resolve an SSID's
// UUID by name (e.g. to import an existing unifi_wifi_broadcast without
// hand-hunting controller IDs). The two union fields on the overview model
// (security, network reference) are deliberately not exposed; a name-to-id
// resolver does not need them.
func NewWifiBroadcastsDataSource() datasource.DataSource {
	return &wifiBroadcastsDataSource{}
}

type wifiBroadcastsDataSource struct {
	data *providerdata.Data
}

type wifiBroadcastsModel struct {
	WifiBroadcasts []wifiBroadcastOverviewModel `tfsdk:"wifi_broadcasts"`
}

type wifiBroadcastOverviewModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Type    types.String `tfsdk:"type"`
	Origin  types.String `tfsdk:"origin"`
}

func (d *wifiBroadcastsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wifi_broadcasts"
}

func (d *wifiBroadcastsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All WiFi broadcasts (SSIDs) on the site. Use it to resolve an SSID's UUID by " +
			"name, e.g. to feed an import of an existing unifi_wifi_broadcast.",
		Attributes: map[string]schema.Attribute{
			"wifi_broadcasts": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The SSIDs.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":      schema.StringAttribute{Computed: true, Description: "WiFi broadcast UUID."},
						"name":    schema.StringAttribute{Computed: true, Description: "SSID name."},
						"enabled": schema.BoolAttribute{Computed: true, Description: "Whether the SSID is enabled."},
						"type":    schema.StringAttribute{Computed: true, Description: "SSID kind (e.g. STANDARD)."},
						"origin":  schema.StringAttribute{Computed: true, Description: "Entity origin (e.g. USER, DERIVED)."},
					},
				},
			},
		},
	}
}

func (d *wifiBroadcastsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *wifiBroadcastsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	broadcasts, err := official.Collect(d.data.Client.Official().WifiBroadcasts().ListAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list wifi broadcasts", err.Error())
		return
	}
	var state wifiBroadcastsModel
	for _, b := range broadcasts {
		state.WifiBroadcasts = append(state.WifiBroadcasts, wifiBroadcastOverviewModel{
			ID:      types.StringValue(b.Id.String()),
			Name:    types.StringValue(b.Name),
			Enabled: types.BoolValue(b.Enabled),
			Type:    types.StringValue(b.Type),
			Origin:  types.StringValue(b.Metadata.Origin),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
