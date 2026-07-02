# A reusable IPv4 list (host, subnet, and range) that firewall policies can reference.
resource "unifi_traffic_matching_list" "blocked_sources" {
  name = "Blocked sources"
  type = "IPV4_ADDRESSES"

  items = [
    {
      match_type = "SUBNET"
      value      = "192.0.2.0/24"
    },
    {
      match_type = "IP_ADDRESS"
      value      = "198.51.100.10"
    },
    {
      match_type = "IP_ADDRESS_RANGE"
      start      = "203.0.113.10"
      stop       = "203.0.113.20"
    },
  ]
}
