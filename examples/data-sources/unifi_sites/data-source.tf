# List all local UniFi sites, e.g. to resolve a site's UUID from its display name.
data "unifi_sites" "all" {}

output "site_ids" {
  value = { for s in data.unifi_sites.all.sites : s.name => s.id }
}
