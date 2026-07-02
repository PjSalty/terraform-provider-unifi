# List all firewall zones, then resolve the SYSTEM-defined zones ("Internal",
# "External", ...) whose UUIDs a firewall policy must reference. System zones
# cannot be created by a resource, so this is the only way to get their IDs.
data "unifi_firewall_zones" "all" {}

locals {
  # Map SYSTEM-defined zone name => UUID.
  system_zones = {
    for z in data.unifi_firewall_zones.all.firewall_zones :
    z.name => z.id if z.origin == "SYSTEM"
  }
}

# Block Internal -> External so IoT clients can't reach the internet, using the
# resolved system zone IDs.
resource "unifi_firewall_policy" "iot_no_internet" {
  name    = "IoT No Internet"
  enabled = true

  action = {
    type = "BLOCK"
  }

  ip_protocol_scope = {
    ip_version = "IPV4_AND_IPV6"
  }

  source = {
    zone_id = local.system_zones["Internal"]
  }

  destination = {
    zone_id = local.system_zones["External"]
  }
}
