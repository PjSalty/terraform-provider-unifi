# List all SSIDs, e.g. to resolve an existing SSID's UUID for an import block
# without hand-hunting controller IDs.
data "unifi_wifi_broadcasts" "all" {}

locals {
  ssid_id = {
    for b in data.unifi_wifi_broadcasts.all.wifi_broadcasts : b.name => b.id
  }
}

import {
  to = unifi_wifi_broadcast.byte_me
  id = local.ssid_id["Byte me"]
}
