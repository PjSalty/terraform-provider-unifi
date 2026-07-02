---
page_title: "Importing Existing Resources - UniFi Provider"
subcategory: "Guides"
description: |-
  How to adopt an existing UniFi controller's networks and SSIDs into Terraform state.
---

# Importing Existing Resources

If you already have a UniFi controller configured by hand (or by another tool), you can bring its objects under Terraform management without recreating them. Every resource in this provider supports import through the `terraform import` command or the `import` block (Terraform 1.5+).

Adoption is a read-then-reconcile loop: point Terraform at an existing object, let it read the live state, then edit your configuration until `terraform plan` reports no changes. Nothing is created or destroyed along the way.

## Finding object IDs

Every object on the UniFi controller is addressed by a UUID, and that UUID is the import ID for its resource. You can find it two ways:

- **Controller UI** - open the object (a network, an SSID, a firewall zone) in the UniFi Network application. The UUID appears in the browser URL.
- **Integration API** - query the same API the provider uses. Auth is the `X-API-KEY` header; the base path is `/proxy/network/integration/v1`.

| Resource | Import ID | API list endpoint (relative to the site) |
|---|---|---|
| `unifi_network` | Network UUID | `/networks` |
| `unifi_wifi_broadcast` | SSID UUID | `/wifi/broadcasts` |
| `unifi_acl_rule` | Rule UUID | `/acl-rules` |
| `unifi_dns_policy` | Policy UUID | `/dns/policies` |
| `unifi_firewall_zone` | Zone UUID | `/firewall/zones` |
| `unifi_hotspot_voucher` | Voucher UUID | `/hotspot/vouchers` |
| `unifi_traffic_matching_list` | List UUID | `/traffic-matching-lists` |

The Integration API addresses objects by site UUID, not by the site name you pass to the provider. List the sites first, grab the UUID of the one you manage, then list its objects:

```bash
# UNIFI_API and UNIFI_API_KEY are the same values the provider reads.
export UNIFI_API="https://unifi.example.com"

# 1. Resolve the site UUID.
curl -sk -H "X-API-KEY: $UNIFI_API_KEY" \
  "$UNIFI_API/proxy/network/integration/v1/sites" | jq '.data[] | {id, name}'

SITE="<site-uuid-from-above>"

# 2. Networks (VLANs) with their UUIDs.
curl -sk -H "X-API-KEY: $UNIFI_API_KEY" \
  "$UNIFI_API/proxy/network/integration/v1/sites/$SITE/networks" | jq '.data[] | {id, name, vlanId}'

# 3. SSIDs with their UUIDs.
curl -sk -H "X-API-KEY: $UNIFI_API_KEY" \
  "$UNIFI_API/proxy/network/integration/v1/sites/$SITE/wifi/broadcasts" | jq '.data[] | {id, name}'
```

(Drop `-k` once the console presents a trusted certificate; it's only there for the self-signed default.)

## Method 1: CLI import (Terraform < 1.5)

Write a resource block that names the object, then import it by UUID:

```terraform
resource "unifi_network" "iot" {
  name    = "IoT"
  vlan_id = 20
}
```

```bash
terraform import unifi_network.iot 3f2504e0-4f89-41d3-9a0c-0305e82c3301
```

Run `terraform plan` afterward to see whether your configuration matches the live object, and adjust it until the plan is clean.

## Method 2: Import blocks (Terraform 1.5+)

The `import` block is declarative and can be committed to version control alongside the resource it targets:

```terraform
import {
  to = unifi_network.iot
  id = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
}

resource "unifi_network" "iot" {
  name    = "IoT"
  vlan_id = 20
}

import {
  to = unifi_wifi_broadcast.corp
  id = "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
}

resource "unifi_wifi_broadcast" "corp" {
  name       = "Corp WiFi"
  security   = "WPA2_WPA3_PERSONAL"
  passphrase = var.corp_passphrase
}
```

Run `terraform plan` to preview the import, then `terraform apply` to write the objects into state.

## Method 3: Generated configuration (Terraform 1.5+)

For more than a couple of objects, let Terraform draft the resource blocks. Write only the `import` blocks:

```terraform
import {
  to = unifi_network.iot
  id = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
}

import {
  to = unifi_network.guest
  id = "3f2504e0-4f89-41d3-9a0c-0305e82c3302"
}
```

Then generate the configuration:

```bash
terraform plan -generate-config-out=generated.tf
```

Review `generated.tf`, clean it up, and fold it into your main configuration.

~> **SSIDs need a hand.** The controller never returns a WiFi passphrase, so `-generate-config-out` can't capture `security` or `passphrase` for a `unifi_wifi_broadcast`. Generated output covers the reliably-readable fields (`name`, `enabled`, `hide_name`, `client_isolation_enabled`); you must add `security`, `passphrase`, and any band, network, or device-filter bindings by hand. `unifi_network` round-trips fully and generates cleanly.

## Adopting a complete site

A realistic pass over an existing controller.

### Step 1: Keep the safety rails on

Set `destroy_protection` while you adopt. If a name or attribute mismatch ever makes Terraform plan a replacement (delete + create), the delete is blocked and you see it in the plan instead of losing a live network or SSID:

```terraform
provider "unifi" {
  api_url            = "https://unifi.example.com"
  destroy_protection = true # blocks deletes; drop it once the plan is clean
}
```

`read_only = true` is even stricter - it blocks every write, so imports and reads still work but nothing can be mutated. Import itself only reads, so both rails are safe to leave on during adoption.

### Step 2: Audit what exists

```bash
curl -sk -H "X-API-KEY: $UNIFI_API_KEY" \
  "$UNIFI_API/proxy/network/integration/v1/sites/$SITE/networks" | jq '.data[] | {id, name, vlanId}'

curl -sk -H "X-API-KEY: $UNIFI_API_KEY" \
  "$UNIFI_API/proxy/network/integration/v1/sites/$SITE/wifi/broadcasts" | jq '.data[] | {id, name}'
```

### Step 3: Write import blocks and stub resources

```terraform
import {
  to = unifi_network.iot
  id = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
}

resource "unifi_network" "iot" {
  name    = "IoT"
  vlan_id = 20
}

import {
  to = unifi_wifi_broadcast.iot
  id = "1ff8a7b5-8c2e-4c1a-9f3d-6a5b4c3d2e1f"
}

resource "unifi_wifi_broadcast" "iot" {
  name       = "IoT Devices"
  security   = "WPA2_PERSONAL"
  passphrase = var.iot_passphrase
  network_id = unifi_network.iot.id
}
```

### Step 4: Reconcile

```bash
terraform plan
```

Anything the plan shows as changing is an attribute your configuration hasn't matched yet. Add the real values until the diff clears. Fields that commonly need to be spelled out after import:

- `enabled`, `hide_name`, `client_isolation_enabled` - defaults may differ from the live object
- `pmf_mode` and `broadcasting_frequencies_ghz` on an SSID
- `network_id` and `broadcasting_device_filter` bindings (device filters are AP UUIDs; resolve them with the `unifi_devices` data source)

### Step 5: Apply, then iterate to a clean plan

```bash
terraform apply
terraform plan
# Plan: 0 to add, 0 to change, 0 to destroy.
```

Repeat until `terraform plan` reports no changes. That's the signal the configuration and the controller agree.

### Step 6: Remove the import blocks

Once the first `apply` succeeds, the objects live in state and the `import` blocks have done their job. Delete them (they're inert on later runs, but leaving them is clutter) and drop `destroy_protection` if you want Terraform to manage the full lifecycle going forward.

## Tips for a smooth adoption

**Import networks before SSIDs.** A `unifi_wifi_broadcast` references its network by UUID, so having the `unifi_network` in state lets you wire `network_id = unifi_network.<name>.id` instead of pasting a raw UUID.

**Import one object at a time.** Run `terraform plan` after each import so a discrepancy surfaces before you pile the next one on top.

**Inspect imported state with `terraform state show`:**

```bash
terraform state show unifi_network.iot
```

**Don't edit an object mid-adoption.** Finish the import and reach a clean plan before you change anything, so Terraform's first real diff is intentional rather than a mix of drift and your edits.

**Treat `id` as computed.** The UUID is assigned by the controller; it's the import ID, never something you set in configuration.
