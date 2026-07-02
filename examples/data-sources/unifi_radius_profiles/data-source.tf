# Resolve a RADIUS profile's UUID by name to attach to a WPA-Enterprise SSID.
data "unifi_radius_profiles" "all" {}

locals {
  corp_radius_id = one([
    for p in data.unifi_radius_profiles.all.radius_profiles : p.id
    if p.name == "corp-radius"
  ])
}

output "corp_radius_profile_id" {
  value = local.corp_radius_id
}
