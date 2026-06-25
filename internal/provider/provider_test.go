package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
)

func TestProviderMetadata(t *testing.T) {
	p := New("v1.2.3")()
	var resp provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &resp)
	if resp.TypeName != "unifi" {
		t.Errorf("TypeName = %q, want unifi", resp.TypeName)
	}
	if resp.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", resp.Version)
	}
}

func TestProviderSchemaHasAuthAttributes(t *testing.T) {
	p := New("test")()
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, attr := range []string{"api_url", "api_key", "site", "allow_insecure", "read_only", "destroy_protection"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing provider attribute %q", attr)
		}
	}
	if !resp.Schema.Attributes["api_key"].IsSensitive() {
		t.Error("api_key must be marked sensitive")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "third"); got != "third" {
		t.Errorf("firstNonEmpty = %q, want third", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("firstNonEmpty = %q, want first", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}
