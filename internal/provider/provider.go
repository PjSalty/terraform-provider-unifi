package provider

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi"

	"github.com/PjSalty/terraform-provider-unifi/internal/datasources"
	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
	"github.com/PjSalty/terraform-provider-unifi/internal/resources"
)

var _ provider.Provider = &UniFiProvider{}

// UniFiProvider manages a self-hosted UniFi Network controller via the official
// Integration API (/proxy/network/integration/v1/, X-API-KEY), which is served
// only by UniFi OS (UDM/Cloud Key/UniFi OS Server) on Network >= 10.1.78.
type UniFiProvider struct {
	version string
}

// UniFiProviderModel describes the provider configuration. Auth is API-key only:
// the official API rejects username/password, and the URL must be https.
type UniFiProviderModel struct {
	APIURL            types.String `tfsdk:"api_url"`
	APIKey            types.String `tfsdk:"api_key"`
	Site              types.String `tfsdk:"site"`
	AllowInsecure     types.Bool   `tfsdk:"allow_insecure"`
	ReadOnly          types.Bool   `tfsdk:"read_only"`
	DestroyProtection types.Bool   `tfsdk:"destroy_protection"`
}

// New returns the provider factory consumed by main.go.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &UniFiProvider{version: version}
	}
}

func (p *UniFiProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "unifi"
	resp.Version = p.version
}

func (p *UniFiProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a self-hosted UniFi Network controller (UniFi OS, Network >= 10.1.78) through the official Integration API with an X-API-KEY.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the UniFi OS console. https only (e.g. https://unifi.example.com). Env: UNIFI_API.",
			},
			"api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Integration API key sent as X-API-KEY. Env: UNIFI_API_KEY. Username/password is not supported by the official API.",
			},
			"site": schema.StringAttribute{
				Optional:    true,
				Description: "Site to manage, resolved to its UUID. Defaults to \"default\". Env: UNIFI_SITE.",
			},
			"allow_insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip TLS verification for the console's self-signed certificate. Env: UNIFI_INSECURE.",
			},
			"read_only": schema.BoolAttribute{
				Optional:    true,
				Description: "Block all create/update/delete operations (safety rail). Env: UNIFI_READ_ONLY.",
			},
			"destroy_protection": schema.BoolAttribute{
				Optional:    true,
				Description: "Block delete operations (safety rail). Env: UNIFI_DESTROY_PROTECTION.",
			},
		},
	}
}

func (p *UniFiProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg UniFiProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiURL := firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("UNIFI_API"))
	apiKey := firstNonEmpty(cfg.APIKey.ValueString(), os.Getenv("UNIFI_API_KEY"))
	site := firstNonEmpty(cfg.Site.ValueString(), os.Getenv("UNIFI_SITE"), "default")
	allowInsecure := cfg.AllowInsecure.ValueBool() || os.Getenv("UNIFI_INSECURE") == "true"

	if apiURL == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_url"), "Missing api_url",
			"Set the api_url argument or the UNIFI_API environment variable.")
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing api_key",
			"Set the api_key argument or the UNIFI_API_KEY environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// APIStyleNew pins the UniFi OS path layout and skips the legacy style probe;
	// SkipSystemInfo defers the connectivity round-trip to the official gate below.
	client, err := unifi.NewClient(&unifi.ClientConfig{
		URL:            apiURL,
		APIKey:         apiKey,
		SkipVerifySSL:  allowInsecure,
		APIStyle:       unifi.APIStyleNew,
		SkipSystemInfo: true,
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to build the UniFi client", err.Error())
		return
	}

	// Resolving the site name to its UUID is the first official-API call, so it
	// triggers the capability gate (new-style controller running Network >=
	// 10.1.78). A gate failure means this controller cannot serve the official
	// API, the bare Network Application container is the usual cause.
	siteID, err := client.Official().Sites().ResolveID(ctx, site)
	if err != nil {
		if errors.Is(err, unifi.ErrOfficialAPIUnavailable) {
			resp.Diagnostics.AddError("UniFi official API unavailable",
				fmt.Sprintf("This provider requires a UniFi OS controller running Network >= 10.1.78 with X-API-KEY auth (UDM, Cloud Key, or UniFi OS Server). %v", err))
		} else {
			resp.Diagnostics.AddError("Failed to resolve the UniFi site",
				fmt.Sprintf("Could not resolve site %q: %v", site, err))
		}
		return
	}

	data := &providerdata.Data{
		Client:            client,
		SiteID:            siteID,
		Site:              site,
		ReadOnly:          cfg.ReadOnly.ValueBool() || os.Getenv("UNIFI_READ_ONLY") == "true",
		DestroyProtection: cfg.DestroyProtection.ValueBool() || os.Getenv("UNIFI_DESTROY_PROTECTION") == "true",
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *UniFiProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewNetworkResource,
		resources.NewWifiBroadcastResource,
		resources.NewDNSPolicyResource,
		resources.NewACLRuleResource,
		resources.NewTrafficMatchingListResource,
		resources.NewHotspotVoucherResource,
		resources.NewFirewallZoneResource,
		resources.NewFirewallPolicyResource,
	}
}

// DataSources returns the read-only lookups: adopted devices/clients plus the
// reference sources (sites, networks, firewall zones, RADIUS profiles, WANs,
// device tags, DPI apps/categories, wifi broadcasts) that resolve names to
// the UUIDs the resources reference.
func (p *UniFiProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewDevicesDataSource,
		datasources.NewClientsDataSource,
		datasources.NewSitesDataSource,
		datasources.NewNetworksDataSource,
		datasources.NewFirewallZonesDataSource,
		datasources.NewRadiusProfilesDataSource,
		datasources.NewWansDataSource,
		datasources.NewDeviceTagsDataSource,
		datasources.NewDPIApplicationsDataSource,
		datasources.NewDpiApplicationCategoriesDataSource,
		datasources.NewWifiBroadcastsDataSource,
	}
}

// firstNonEmpty returns the first non-empty string, used for the
// argument-then-environment fallback on each provider setting.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
