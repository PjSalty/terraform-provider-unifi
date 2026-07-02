# A WPA2-only IoT SSID on one specific AP, bound to a VLAN.
# WPA2_PERSONAL (not transition) keeps legacy IoT gear that can't do WPA3 happy.

data "unifi_devices" "all" {}

locals {
  # Target one AP by its name, as shown in the UniFi console.
  iot_ap = one([for d in data.unifi_devices.all.devices : d.id if d.name == "Office AP"])
}

resource "unifi_wifi_broadcast" "iot" {
  name                         = "IoT Devices"
  security                     = "WPA2_PERSONAL"
  passphrase                   = var.iot_passphrase
  pmf_mode                     = "OPTIONAL"
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.iot.id
  broadcasting_device_filter   = [local.iot_ap]
  client_isolation_enabled     = true
}
