package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// clearProviderEnv blanks every UNIFI_* variable Configure reads so the
// developer's real environment cannot leak into a test.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"UNIFI_API", "UNIFI_API_KEY", "UNIFI_SITE", "UNIFI_INSECURE", "UNIFI_READ_ONLY", "UNIFI_DESTROY_PROTECTION"} {
		t.Setenv(k, "")
	}
}

// providerConfigType mirrors the provider schema for hand-built raw values.
var providerConfigType = tftypes.Object{
	AttributeTypes: map[string]tftypes.Type{
		"api_url":            tftypes.String,
		"api_key":            tftypes.String,
		"site":               tftypes.String,
		"allow_insecure":     tftypes.Bool,
		"read_only":          tftypes.Bool,
		"destroy_protection": tftypes.Bool,
	},
}

func providerSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp provider.SchemaResponse
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// configureReq builds a ConfigureRequest whose config carries the given
// attribute values; every attribute not listed is null.
func configureReq(t *testing.T, vals map[string]tftypes.Value) provider.ConfigureRequest {
	t.Helper()
	raw := map[string]tftypes.Value{
		"api_url":            tftypes.NewValue(tftypes.String, nil),
		"api_key":            tftypes.NewValue(tftypes.String, nil),
		"site":               tftypes.NewValue(tftypes.String, nil),
		"allow_insecure":     tftypes.NewValue(tftypes.Bool, nil),
		"read_only":          tftypes.NewValue(tftypes.Bool, nil),
		"destroy_protection": tftypes.NewValue(tftypes.Bool, nil),
	}
	for k, v := range vals {
		raw[k] = v
	}
	return provider.ConfigureRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(providerConfigType, raw),
		Schema: providerSchema(t),
	}}
}

func configure(t *testing.T, req provider.ConfigureRequest) *provider.ConfigureResponse {
	t.Helper()
	var resp provider.ConfigureResponse
	New("test")().Configure(context.Background(), req, &resp)
	return &resp
}

// errorSummaries collects the summaries of all error diagnostics.
func errorSummaries(resp *provider.ConfigureResponse) []string {
	var out []string
	for _, d := range resp.Diagnostics.Errors() {
		out = append(out, d.Summary())
	}
	return out
}

func hasErrorSummary(resp *provider.ConfigureResponse, want string) bool {
	for _, s := range errorSummaries(resp) {
		if s == want {
			return true
		}
	}
	return false
}

// TestConfigureBadConfig proves a config whose raw value does not match the
// provider schema surfaces Get diagnostics and stops before any client work.
func TestConfigureBadConfig(t *testing.T) {
	clearProviderEnv(t)
	badType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"api_url": tftypes.Bool,
	}}
	req := provider.ConfigureRequest{Config: tfsdk.Config{
		Raw: tftypes.NewValue(badType, map[string]tftypes.Value{
			"api_url": tftypes.NewValue(tftypes.Bool, true),
		}),
		Schema: providerSchema(t),
	}}
	resp := configure(t, req)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a mismatched config value")
	}
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Error("provider data must not be set after a config decode failure")
	}
}

// TestConfigureMissingURLAndKey proves both required settings are reported
// together when neither the config nor the environment provides them.
func TestConfigureMissingURLAndKey(t *testing.T) {
	clearProviderEnv(t)
	resp := configure(t, configureReq(t, nil))
	if got := resp.Diagnostics.ErrorsCount(); got != 2 {
		t.Fatalf("errors = %d (%v), want 2", got, errorSummaries(resp))
	}
	if !hasErrorSummary(resp, "Missing api_url") {
		t.Errorf("missing api_url diagnostic, got %v", errorSummaries(resp))
	}
	if !hasErrorSummary(resp, "Missing api_key") {
		t.Errorf("missing api_key diagnostic, got %v", errorSummaries(resp))
	}
}

// TestConfigureMissingAPIKey proves a URL-only config fails on the key alone.
func TestConfigureMissingAPIKey(t *testing.T) {
	clearProviderEnv(t)
	resp := configure(t, configureReq(t, map[string]tftypes.Value{
		"api_url": tftypes.NewValue(tftypes.String, "https://192.0.2.1"),
	}))
	if got := resp.Diagnostics.ErrorsCount(); got != 1 {
		t.Fatalf("errors = %d (%v), want 1", got, errorSummaries(resp))
	}
	if !hasErrorSummary(resp, "Missing api_key") {
		t.Errorf("missing api_key diagnostic, got %v", errorSummaries(resp))
	}
}

// TestConfigureRejectsHTTPURL proves client construction fails for a non-https
// URL (the official API is https-only).
func TestConfigureRejectsHTTPURL(t *testing.T) {
	clearProviderEnv(t)
	resp := configure(t, configureReq(t, map[string]tftypes.Value{
		"api_url": tftypes.NewValue(tftypes.String, "http://192.0.2.1"),
		"api_key": tftypes.NewValue(tftypes.String, "test-key"),
	}))
	if !hasErrorSummary(resp, "Failed to build the UniFi client") {
		t.Fatalf("expected client build error, got %v", errorSummaries(resp))
	}
}

// TestConfigureOfficialAPIUnavailable proves a controller that cannot serve the
// official API (here: the capability probe fails outright) maps to the
// dedicated ErrOfficialAPIUnavailable diagnostic.
func TestConfigureOfficialAPIUnavailable(t *testing.T) {
	clearProviderEnv(t)
	s := testutil.NewSitesServer(t, nil)
	url := s.URL
	// Close before Configure so the capability probe (GET /info) fails.
	s.Close()
	resp := configure(t, configureReq(t, map[string]tftypes.Value{
		"api_url":        tftypes.NewValue(tftypes.String, url),
		"api_key":        tftypes.NewValue(tftypes.String, "test-key"),
		"allow_insecure": tftypes.NewValue(tftypes.Bool, true),
	}))
	if !hasErrorSummary(resp, "UniFi official API unavailable") {
		t.Fatalf("expected official-API-unavailable error, got %v", errorSummaries(resp))
	}
}

// TestConfigureUnknownSite proves a resolvable controller with no matching site
// falls into the generic resolve-failure branch, naming the site.
func TestConfigureUnknownSite(t *testing.T) {
	clearProviderEnv(t)
	s := testutil.NewSitesServer(t, nil)
	resp := configure(t, configureReq(t, map[string]tftypes.Value{
		"api_url":        tftypes.NewValue(tftypes.String, s.URL),
		"api_key":        tftypes.NewValue(tftypes.String, "test-key"),
		"site":           tftypes.NewValue(tftypes.String, "branch-office"),
		"allow_insecure": tftypes.NewValue(tftypes.Bool, true),
	}))
	if !hasErrorSummary(resp, "Failed to resolve the UniFi site") {
		t.Fatalf("expected site resolve error, got %v", errorSummaries(resp))
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, `"branch-office"`) {
		t.Errorf("detail %q does not name the site", detail)
	}
}

// TestConfigureHappyPath proves a full config wires resolved provider data into
// both ResourceData and DataSourceData, guards included.
func TestConfigureHappyPath(t *testing.T) {
	clearProviderEnv(t)
	s := testutil.NewSitesServer(t, nil)
	resp := configure(t, configureReq(t, map[string]tftypes.Value{
		"api_url":            tftypes.NewValue(tftypes.String, s.URL),
		"api_key":            tftypes.NewValue(tftypes.String, "test-key"),
		"allow_insecure":     tftypes.NewValue(tftypes.Bool, true),
		"read_only":          tftypes.NewValue(tftypes.Bool, true),
		"destroy_protection": tftypes.NewValue(tftypes.Bool, true),
	}))
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", errorSummaries(resp))
	}
	data, ok := resp.ResourceData.(*providerdata.Data)
	if !ok {
		t.Fatalf("ResourceData is %T, want *providerdata.Data", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("DataSourceData and ResourceData must be the same provider data")
	}
	if data.Client == nil {
		t.Error("Client is nil")
	}
	if data.SiteID != testutil.SiteID {
		t.Errorf("SiteID = %s, want %s", data.SiteID, testutil.SiteID)
	}
	if data.Site != "default" {
		t.Errorf("Site = %q, want default", data.Site)
	}
	if !data.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
	if !data.DestroyProtection {
		t.Error("DestroyProtection = false, want true")
	}
	if s.Requests == 0 {
		t.Error("site resolution never hit the controller")
	}
}

// TestConfigureEnvFallback proves every setting falls back to its UNIFI_*
// environment variable when the config leaves it null.
func TestConfigureEnvFallback(t *testing.T) {
	s := testutil.NewSitesServer(t, nil)
	t.Setenv("UNIFI_API", s.URL)
	t.Setenv("UNIFI_API_KEY", "env-key")
	t.Setenv("UNIFI_SITE", "default")
	t.Setenv("UNIFI_INSECURE", "true")
	t.Setenv("UNIFI_READ_ONLY", "true")
	t.Setenv("UNIFI_DESTROY_PROTECTION", "true")
	resp := configure(t, configureReq(t, nil))
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", errorSummaries(resp))
	}
	data, ok := resp.ResourceData.(*providerdata.Data)
	if !ok {
		t.Fatalf("ResourceData is %T, want *providerdata.Data", resp.ResourceData)
	}
	if data.Site != "default" || data.SiteID != testutil.SiteID {
		t.Errorf("site = %q/%s, want default/%s", data.Site, data.SiteID, testutil.SiteID)
	}
	if !data.ReadOnly || !data.DestroyProtection {
		t.Errorf("guards = %v/%v, want both true from env", data.ReadOnly, data.DestroyProtection)
	}
}

// TestResourcesAndDataSources proves both registries return working factories.
// 8 resources (network, wifi_broadcast, dns_policy, acl_rule,
// traffic_matching_list, hotspot_voucher, firewall_zone, firewall_policy) and
// 10 data sources.
func TestResourcesAndDataSources(t *testing.T) {
	p := &UniFiProvider{}
	res := p.Resources(context.Background())
	if len(res) != 8 {
		t.Errorf("resources = %d, want 8", len(res))
	}
	for i, f := range res {
		if f() == nil {
			t.Errorf("resource factory %d returned nil", i)
		}
	}
	ds := p.DataSources(context.Background())
	if len(ds) != 10 {
		t.Errorf("data sources = %d, want 10", len(ds))
	}
	for i, f := range ds {
		if f() == nil {
			t.Errorf("data source factory %d returned nil", i)
		}
	}
}
