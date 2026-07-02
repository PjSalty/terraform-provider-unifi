package testutil

import (
	"context"
	"net/http"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
)

func TestDataDefaults(t *testing.T) {
	oc := &official.ClientMock{}
	d := Data(oc)
	if d.SiteID != SiteID {
		t.Errorf("SiteID = %v, want %v", d.SiteID, SiteID)
	}
	if d.Site != "default" {
		t.Errorf("Site = %q, want default", d.Site)
	}
	if d.ReadOnly || d.DestroyProtection {
		t.Errorf("guards should default off, got ro=%v dp=%v", d.ReadOnly, d.DestroyProtection)
	}
	if got := d.Client.Official(); got != official.Client(oc) {
		t.Errorf("Official() did not return the supplied mock")
	}
}

func TestDataOpts(t *testing.T) {
	d := Data(&official.ClientMock{}, ReadOnly(), DestroyProtection())
	if !d.ReadOnly {
		t.Error("ReadOnly opt not applied")
	}
	if !d.DestroyProtection {
		t.Error("DestroyProtection opt not applied")
	}
}

func TestNewSitesServerDefault(t *testing.T) {
	srv := NewSitesServer(t, nil)
	client, err := unifi.NewClient(&unifi.ClientConfig{
		URL:            srv.URL,
		APIKey:         "test-key",
		APIStyle:       unifi.APIStyleNew,
		SkipVerifySSL:  true,
		SkipSystemInfo: true,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	id, err := client.Official().Sites().ResolveID(context.Background(), "default")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if id != SiteID {
		t.Errorf("ResolveID = %v, want %v", id, SiteID)
	}
	if srv.Requests == 0 {
		t.Error("server saw no requests")
	}
}

func TestNewSitesServerHook(t *testing.T) {
	srv := NewSitesServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client, err := unifi.NewClient(&unifi.ClientConfig{
		URL:            srv.URL,
		APIKey:         "test-key",
		APIStyle:       unifi.APIStyleNew,
		SkipVerifySSL:  true,
		SkipSystemInfo: true,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Official().Sites().ResolveID(context.Background(), "default"); err == nil {
		t.Fatal("expected error from 500 hook, got nil")
	}
}
