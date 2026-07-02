# A VLAN-only network (an 802.1Q tag) for an IoT segment.
resource "unifi_network" "iot" {
  name    = "IoT Devices"
  vlan_id = 20
  enabled = true
}
