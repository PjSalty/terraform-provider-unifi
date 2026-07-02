package providerdata

import (
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
)

// TestDataCarriesConfiguration proves the struct hands every resolved provider
// setting through unchanged: the client, the site identity pair, and both
// safety guards.
func TestDataCarriesConfiguration(t *testing.T) {
	id := uuid.MustParse("00000000-0000-4000-8000-000000000042")
	client := &unifi.ClientMock{OfficialFunc: func() official.Client { return nil }}
	d := &Data{
		Client:            client,
		SiteID:            id,
		Site:              "default",
		ReadOnly:          true,
		DestroyProtection: true,
	}
	if d.Client != unifi.Client(client) {
		t.Error("Client not carried through")
	}
	if d.SiteID != id {
		t.Errorf("SiteID = %s, want %s", d.SiteID, id)
	}
	if d.Site != "default" {
		t.Errorf("Site = %q, want default", d.Site)
	}
	if !d.ReadOnly || !d.DestroyProtection {
		t.Errorf("guards = %v/%v, want both true", d.ReadOnly, d.DestroyProtection)
	}
}

// TestDataZeroValueIsPermissive proves the zero value leaves both guards off,
// matching the provider's default (no read_only, no destroy_protection).
func TestDataZeroValueIsPermissive(t *testing.T) {
	var d Data
	if d.ReadOnly {
		t.Error("zero-value ReadOnly = true, want false")
	}
	if d.DestroyProtection {
		t.Error("zero-value DestroyProtection = true, want false")
	}
}
