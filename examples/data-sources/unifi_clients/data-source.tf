# List every client currently connected to the site.
data "unifi_clients" "all" {}

# The wireless clients, with the IP and MAC the controller reported for each.
output "wireless_clients" {
  value = [
    for c in data.unifi_clients.all.clients : {
      name = c.name
      ip   = c.ip_address
      mac  = c.mac_address
    } if c.type == "WIRELESS"
  ]
}
