# List configured WANs, e.g. to resolve a WAN's UUID for other resources.
data "unifi_wans" "all" {}

output "wans_by_name" {
  value = { for w in data.unifi_wans.all.wans : w.name => w.id }
}
