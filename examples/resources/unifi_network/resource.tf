# A VLAN-only network (an 802.1Q tag) for an IoT segment. An external router
# (not a UniFi gateway) handles L3 for this VLAN.
resource "unifi_network" "iot" {
  name    = "IoT Devices"
  vlan_id = 20
  enabled = true
}

# A gateway-managed network: the UniFi gateway routes the subnet and runs a
# DHCP server. Only use this when a UniFi gateway is the router for the VLAN.
resource "unifi_network" "corp" {
  name       = "Corp"
  vlan_id    = 10
  management = "GATEWAY"

  gateway = {
    host_ip_address = "192.168.10.1"
    prefix_length   = 24

    dhcp = {
      range_start        = "192.168.10.100"
      range_stop         = "192.168.10.200"
      dns_servers        = ["10.10.20.13"]
      domain_name        = "corp.lan"
      lease_time_seconds = 86400
    }
  }
}
