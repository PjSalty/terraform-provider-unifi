# A firewall zone grouping the IoT network so zone-based policies can target it.
resource "unifi_network" "iot" {
  name    = "IoT Devices"
  vlan_id = 20
  enabled = true
}

resource "unifi_firewall_zone" "iot" {
  name        = "IoT"
  network_ids = [unifi_network.iot.id]
}
