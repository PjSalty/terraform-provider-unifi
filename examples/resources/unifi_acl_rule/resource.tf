# An IPV4 ACL rule blocking a guest subnet from reaching an internal host on SSH and SMB.
resource "unifi_acl_rule" "block_guest_to_server" {
  type    = "IPV4"
  name    = "Block guest to file server"
  action  = "BLOCK"
  enabled = true

  protocol_filter = ["TCP"]

  source_filter = {
    ip_addresses_or_subnets = ["192.0.2.0/24"]
  }

  destination_filter = {
    ip_addresses_or_subnets = ["198.51.100.10"]
    ports                   = [22, 445]
  }
}
