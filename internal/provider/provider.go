package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

// ProviderData is the resolved configuration handed to every resource and data
// source through Configure. The go-unifi/v2 official client is added to this in
// Step 2.
type ProviderData struct {
	APIURL            string
	APIKey            string
	Site              string
	AllowInsecure     bool
	ReadOnly          bool
	DestroyProtection bool
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

	data := ProviderData{
		APIURL:            firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("UNIFI_API")),
		APIKey:            firstNonEmpty(cfg.APIKey.ValueString(), os.Getenv("UNIFI_API_KEY")),
		Site:              firstNonEmpty(cfg.Site.ValueString(), os.Getenv("UNIFI_SITE"), "default"),
		AllowInsecure:     cfg.AllowInsecure.ValueBool() || os.Getenv("UNIFI_INSECURE") == "true",
		ReadOnly:          cfg.ReadOnly.ValueBool() || os.Getenv("UNIFI_READ_ONLY") == "true",
		DestroyProtection: cfg.DestroyProtection.ValueBool() || os.Getenv("UNIFI_DESTROY_PROTECTION") == "true",
	}

	if data.APIURL == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_url"), "Missing api_url",
			"Set the api_url argument or the UNIFI_API environment variable.")
	}
	if data.APIKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing api_key",
			"Set the api_key argument or the UNIFI_API_KEY environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// shortcut: Step 1 validates and stores config only. Step 2 wires the
	// go-unifi/v2 official client (unifi.NewClient ... c.Official()), resolves
	// the site name to a UUID, and gates on Network >= 10.1.78. Resources and
	// data sources are added in Steps 3-5.
	resp.ResourceData = &data
	resp.DataSourceData = &data
}

func (p *UniFiProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *UniFiProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
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
