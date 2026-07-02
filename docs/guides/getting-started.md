---
page_title: "Getting Started - UniFi Provider"
subcategory: "Guides"
description: |-
  A step-by-step guide to getting started with the UniFi Terraform provider.
---

# Getting Started

This guide walks you through setting up the UniFi Terraform provider from scratch and creating your first resources.

## Prerequisites

- A UniFi OS console (UDM, Cloud Key, or UniFi OS Server) running UniFi Network 10.1.78 or later
- Terraform 1.5 or later installed locally
- Network access to your UniFi console over HTTPS

~> **Note:** This provider talks to the official Integration API and authenticates with an API key (`X-API-KEY`). A bare Network Application container without UniFi OS does not expose that API. Username/password authentication is not supported.

## Step 1: Create a UniFi Integration API Key

1. Log in to the UniFi console.
2. Navigate to **Settings → Control Plane → Integrations**.
3. Click **Create API Key** and give it a descriptive name (e.g., `terraform`).
4. Copy the generated API key, it is only shown once.

~> **Security Note:** Store this API key in a secrets manager or Terraform variable. Never commit it to version control.

## Step 2: Configure the Provider

Create a new directory for your Terraform configuration:

```bash
mkdir unifi-infra && cd unifi-infra
```

Create `versions.tf`:

```terraform
terraform {
  required_version = ">= 1.5"
  required_providers {
    unifi = {
      source  = "PjSalty/unifi"
      version = "~> 1.0"
    }
  }
}
```

Create `provider.tf`:

```terraform
provider "unifi" {
  api_url = var.unifi_api_url
  api_key = var.unifi_api_key
  # site defaults to "default"
  # allow_insecure = true  # only if the console still uses a self-signed cert
}
```

Create `variables.tf`:

```terraform
variable "unifi_api_url" {
  description = "Base URL of the UniFi OS console (https only)"
  type        = string
}

variable "unifi_api_key" {
  description = "UniFi Integration API key"
  type        = string
  sensitive   = true
}
```

Create `terraform.tfvars` (keep this out of version control):

```hcl
unifi_api_url = "https://unifi.example.com"
unifi_api_key = "your-api-key-here"
```

Add `terraform.tfvars` to your `.gitignore`:

```
terraform.tfvars
*.tfvars
.terraform/
.terraform.lock.hcl
```

Alternatively, use environment variables:

```bash
export UNIFI_API="https://unifi.example.com"
export UNIFI_API_KEY="your-api-key-here"
```

## Step 3: Create Your First Network (VLAN)

Create `main.tf`:

```terraform
resource "unifi_network" "iot" {
  name    = "IoT Devices"
  vlan_id = 20
  enabled = true
}

output "network_id" {
  value = unifi_network.iot.id
}
```

## Step 4: Initialize and Apply

Initialize Terraform and download the provider:

```bash
terraform init
```

Review what will be created:

```bash
terraform plan
```

Apply the configuration:

```bash
terraform apply
```

Type `yes` when prompted. Terraform will create the network and display the output:

```
Outputs:

network_id = "00000000-0000-0000-0000-000000000000"
```

## Step 5: Verify in the UniFi Console

Log in to the UniFi console and navigate to **Settings → Networks** to confirm the `IoT Devices` network was created on VLAN 20.

## Step 6: Add a WiFi SSID

Broadcast an SSID and bind its clients to the VLAN you just created. Add a variable for the passphrase to `variables.tf`:

```terraform
variable "wifi_passphrase" {
  description = "Pre-shared key for the IoT SSID (8-63 chars)"
  type        = string
  sensitive   = true
}
```

Set it in `terraform.tfvars`:

```hcl
wifi_passphrase = "your-strong-passphrase"
```

Add the SSID to `main.tf`:

```terraform
resource "unifi_wifi_broadcast" "iot" {
  name                         = "IoT Devices"
  security                     = "WPA2_PERSONAL"
  passphrase                   = var.wifi_passphrase
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.iot.id
  client_isolation_enabled     = true
}
```

Run `terraform apply` again to create the SSID. Clients that join `IoT Devices` are placed on VLAN 20 and, with `client_isolation_enabled`, cannot reach each other.

## Cleaning Up

To remove all resources created by this guide:

```bash
terraform destroy
```

## Next Steps

- Browse the resource reference for `unifi_acl_rule`, `unifi_dns_policy`, `unifi_firewall_zone`, `unifi_hotspot_voucher`, and `unifi_traffic_matching_list`
- Use the `unifi_devices` data source to resolve an access point's UUID and target an SSID at specific APs with `broadcasting_device_filter`
- Use the `unifi_clients` data source to inspect connected clients
- Set `read_only = true` or `destroy_protection = true` on the provider as safety rails while you iterate
