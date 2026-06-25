package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

var (
	_ datasource.DataSource              = &clientsDataSource{}
	_ datasource.DataSourceWithConfigure = &clientsDataSource{}
)

// NewClientsDataSource returns the unifi_clients data source, which lists every
// client currently connected to the site (wired, wireless, VPN and Teleport).
func NewClientsDataSource() datasource.DataSource {
	return &clientsDataSource{}
}

type clientsDataSource struct {
	data *providerdata.Data
}

type clientsModel struct {
	Clients []clientModel `tfsdk:"clients"`
}

type clientModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	IPAddress      types.String `tfsdk:"ip_address"`
	MacAddress     types.String `tfsdk:"mac_address"`
	UplinkDeviceID types.String `tfsdk:"uplink_device_id"`
	ConnectedAt    types.String `tfsdk:"connected_at"`
}

func (d *clientsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_clients"
}

func (d *clientsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "All clients currently connected to the UniFi site. Wired and wireless clients expose a MAC address and uplink device; VPN and Teleport clients do not.",
		Attributes: map[string]schema.Attribute{
			"clients": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The connected clients.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, Description: "Client UUID."},
						"name":        schema.StringAttribute{Computed: true, Description: "Client name (hostname or display name)."},
						"type":        schema.StringAttribute{Computed: true, Description: "Connection type: WIRED, WIRELESS, VPN, or TELEPORT."},
						"ip_address":  schema.StringAttribute{Computed: true, Description: "Current IP address. Empty if the controller has not reported one."},
						"mac_address": schema.StringAttribute{Computed: true, Description: "MAC address. Only populated for WIRED and WIRELESS clients; empty otherwise."},
						"uplink_device_id": schema.StringAttribute{
							Computed:    true,
							Description: "UUID of the device this client connects through. Only populated for WIRED and WIRELESS clients; empty otherwise.",
						},
						"connected_at": schema.StringAttribute{Computed: true, Description: "RFC 3339 timestamp the client connected at. Empty if not reported."},
					},
				},
			},
		},
	}
}

func (d *clientsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *clientsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	clients, err := official.Collect(d.data.Client.Official().Clients().ListConnectedAll(ctx, d.data.SiteID, ""))
	if err != nil {
		resp.Diagnostics.AddError("Failed to list clients", err.Error())
		return
	}
	var state clientsModel
	for _, c := range clients {
		m, err := flattenClient(c)
		if err != nil {
			resp.Diagnostics.AddError("Failed to decode client", err.Error())
			return
		}
		state.Clients = append(state.Clients, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// flattenClient maps a ClientOverview into the data-source model.
//
// ClientOverview is a discriminated union keyed on Type. The base fields (Id,
// Name, Type, IpAddress, ConnectedAt) are decoded directly by UnmarshalJSON, but
// MacAddress and UplinkDeviceId live only on the WIRED and WIRELESS variants, so
// they must be pulled out of the union via the As* accessors. VPN and TELEPORT
// carry no MAC, so those columns are left empty for them.
func flattenClient(c official.ClientOverview) (clientModel, error) {
	m := clientModel{
		ID:             types.StringValue(c.Id.String()),
		Name:           types.StringValue(c.Name),
		Type:           types.StringValue(c.Type),
		IPAddress:      types.StringValue(derefString(c.IpAddress)),
		MacAddress:     types.StringValue(""),
		UplinkDeviceID: types.StringValue(""),
		ConnectedAt:    types.StringValue(""),
	}
	if c.ConnectedAt != nil {
		m.ConnectedAt = types.StringValue(c.ConnectedAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}

	switch c.Type {
	case "WIRED":
		wired, err := c.AsWiredClientOverview()
		if err != nil {
			return clientModel{}, fmt.Errorf("decode WIRED client %s: %w", c.Id, err)
		}
		m.MacAddress = types.StringValue(derefString(wired.MacAddress))
		m.UplinkDeviceID = types.StringValue(derefUUID(wired.UplinkDeviceId))
	case "WIRELESS":
		wireless, err := c.AsWirelessClientOverview()
		if err != nil {
			return clientModel{}, fmt.Errorf("decode WIRELESS client %s: %w", c.Id, err)
		}
		m.MacAddress = types.StringValue(derefString(wireless.MacAddress))
		m.UplinkDeviceID = types.StringValue(derefUUID(wireless.UplinkDeviceId))
	}
	// VPN and TELEPORT (and any future variant) carry no MAC/uplink; leave empty.

	return m, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefUUID renders a *uuid.UUID; the go-unifi field type *openapi_types.UUID is
// a true alias of *uuid.UUID, so this signature matches without an extra import.
func derefUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}
