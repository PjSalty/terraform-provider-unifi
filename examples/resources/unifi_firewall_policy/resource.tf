# Block the IoT zone from initiating traffic into the Trusted zone.
resource "unifi_network" "iot" {
  name    = "IoT Devices"
  vlan_id = 20
  enabled = true
}

resource "unifi_network" "trusted" {
  name    = "Trusted LAN"
  vlan_id = 50
  enabled = true
}

resource "unifi_firewall_zone" "iot" {
  name        = "IoT"
  network_ids = [unifi_network.iot.id]
}

resource "unifi_firewall_zone" "trusted" {
  name        = "Trusted"
  network_ids = [unifi_network.trusted.id]
}

resource "unifi_firewall_policy" "block_iot_to_trusted" {
  name            = "Block IoT to Trusted"
  enabled         = true
  logging_enabled = true
  description     = "IoT devices may not initiate connections into the trusted LAN."

  action = {
    type = "BLOCK"
  }

  ip_protocol_scope = {
    ip_version = "IPV4_AND_IPV6"
  }

  source = {
    zone_id = unifi_firewall_zone.iot.id
  }

  destination = {
    zone_id     = unifi_firewall_zone.trusted.id
    network_ids = [unifi_network.trusted.id]
  }
}
