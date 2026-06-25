# List adopted devices, e.g. to resolve an AP's UUID for SSID targeting.
data "unifi_devices" "all" {}

output "access_points" {
  value = [for d in data.unifi_devices.all.devices : "${d.name} (${d.model}) = ${d.id}"]
}
