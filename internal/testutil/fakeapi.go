// Package testutil provides the shared test doubles for unit-testing every
// layer of the provider without a live controller.
//
// Two tools, two jobs:
//
//   - Data wraps the go-unifi generated mocks (official.ClientMock and the
//     per-domain *ClientMock types) into a ready *providerdata.Data, so
//     resource and data-source tests drive Create/Read/Update/Delete with
//     exact per-method control over responses and error branches.
//
//   - NewSitesServer stands up an httptest server speaking just enough of
//     the Integration API ({offset,limit,count,totalCount,data} envelopes
//     under /proxy/network/integration/v1) for provider.Configure tests,
//     which build a real client and resolve the site name.
package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

// SiteID is the fixed site UUID Data wires into providerdata, so tests can
// assert the site every mocked call receives.
var SiteID = uuid.MustParse("00000000-0000-4000-8000-000000000001")

// Opt mutates the Data under construction.
type Opt func(*providerdata.Data)

// ReadOnly marks the provider data read-only.
func ReadOnly() Opt { return func(d *providerdata.Data) { d.ReadOnly = true } }

// DestroyProtection enables the delete guard.
func DestroyProtection() Opt { return func(d *providerdata.Data) { d.DestroyProtection = true } }

// Data returns provider data whose client serves the given official-API mock.
// Any method the test does not stub panics when called, which is exactly the
// failure mode a unit test wants.
func Data(oc official.Client, opts ...Opt) *providerdata.Data {
	d := &providerdata.Data{
		Client:   &unifi.ClientMock{OfficialFunc: func() official.Client { return oc }},
		SiteID:   SiteID,
		Site:     "default",
		ReadOnly: false,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// SitesServer is the minimal Integration-API endpoint set provider.Configure
// needs: a sites list that resolves the requested site name.
type SitesServer struct {
	*httptest.Server
	// Requests counts the sites-list calls served.
	Requests int
}

// NewSitesServer starts a TLS httptest server (the client validates an https
// URL; pair it with SkipVerifySSL) whose GET
// /proxy/network/integration/v1/sites returns one site named "default" with
// SiteID. Non-nil hook overrides the response entirely (error injection).
// The server is closed automatically when the test finishes.
func NewSitesServer(t *testing.T, hook http.HandlerFunc) *SitesServer {
	t.Helper()
	s := &SitesServer{}
	mux := http.NewServeMux()
	// The client's capability gate probes application info before the first
	// official-API call; answer with a gate-passing Network version.
	mux.HandleFunc("/proxy/network/integration/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"applicationVersion":"10.1.78"}`))
	})
	mux.HandleFunc("/proxy/network/integration/v1/sites", func(w http.ResponseWriter, r *http.Request) {
		s.Requests++
		if hook != nil {
			hook(w, r)
			return
		}
		WriteEnvelope(w, []map[string]any{{
			"id":                SiteID.String(),
			"internalReference": "default",
			"name":              "Default",
		}})
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

// WriteEnvelope writes the standard Integration-API list envelope around data.
func WriteEnvelope(w http.ResponseWriter, data []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"offset":     0,
		"limit":      len(data),
		"count":      len(data),
		"totalCount": len(data),
		"data":       data,
	})
}
