# List every network/VLAN on the site, e.g. to reference the default or
# system-defined VLANs the unifi_network resource cannot create.
data "unifi_networks" "all" {}

# Look up an existing VLAN by name (e.g. the "Infrastructure" VLAN).
locals {
  infrastructure = one([for n in data.unifi_networks.all.networks : n if n.name == "Infrastructure"])

  # Or match by VLAN ID (VLAN 30 = Kubernetes).
  kubernetes = one([for n in data.unifi_networks.all.networks : n if n.vlan_id == 30])
}

output "infrastructure_vlan_id" {
  value = local.infrastructure.vlan_id
}

output "kubernetes_network_id" {
  value = local.kubernetes.id
}
