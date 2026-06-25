// Package providerdata carries the resolved provider configuration that the
// provider's Configure hands to every resource and data source. It lives in its
// own package so internal/provider and internal/resources can both import it
// without an import cycle.
package providerdata

import (
	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/google/uuid"
)

// Data is set as the provider's ResourceData and DataSourceData, then
// type-asserted by each resource's Configure.
type Data struct {
	// Client is the go-unifi official-API client.
	Client unifi.Client
	// SiteID is the Official-API site UUID every resource-group call needs.
	SiteID uuid.UUID
	// Site is the human-facing site name SiteID was resolved from.
	Site string
	// ReadOnly blocks all create/update/delete operations.
	ReadOnly bool
	// DestroyProtection blocks delete operations.
	DestroyProtection bool
}
