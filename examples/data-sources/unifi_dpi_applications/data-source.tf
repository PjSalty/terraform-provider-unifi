# List every DPI application the controller recognizes, then build a
# name -> numeric id map so DPI-based rules can reference apps by name.
data "unifi_dpi_applications" "all" {}

output "dpi_app_ids" {
  value = { for app in data.unifi_dpi_applications.all.dpi_applications : app.name => app.id }
}

# Resolve a single application id (e.g. to throttle BitTorrent on the IoT VLAN).
output "bittorrent_app_id" {
  value = one([for app in data.unifi_dpi_applications.all.dpi_applications : app.id if app.name == "BitTorrent"])
}
