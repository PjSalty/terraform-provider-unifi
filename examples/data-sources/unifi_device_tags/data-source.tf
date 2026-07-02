# List device tags, then resolve one by name to its UUID and grouped devices.
data "unifi_device_tags" "all" {}

locals {
  iot_tag = one([for t in data.unifi_device_tags.all.device_tags : t if t.name == "iot-access-points"])
}

output "iot_tag_id" {
  value = local.iot_tag.id
}

output "iot_tag_device_ids" {
  value = local.iot_tag.device_ids
}
