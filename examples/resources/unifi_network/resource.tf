# A VLAN-only network (an 802.1Q tag) for an IoT segment.
resource "unifi_network" "remote_iot" {
  name    = "Remote-IoT"
  vlan_id = 81
  enabled = true
}
