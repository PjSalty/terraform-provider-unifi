---
page_title: "Network Segmentation - UniFi Provider"
subcategory: "Guides"
description: |-
  Building segmented WiFi with per-VLAN networks, SSID-to-VLAN binding, client isolation, and per-AP targeting.
---

# Network Segmentation

This guide shows how to build a segmented wireless network with the UniFi provider: a trusted VLAN for your own devices, an isolated VLAN for IoT gear, and a separate VLAN for guests, each on its own SSID. The goal is to keep untrusted devices off the same broadcast domain as your laptops and phones, and to stop compromised devices from talking to their neighbors.

Three provider primitives do the work:

| Concern | Mechanism | Resource / attribute |
|---|---|---|
| Put a device class on its own broadcast domain | An 802.1Q VLAN | `unifi_network` (`vlan_id`) |
| Send an SSID's clients into that VLAN | SSID-to-VLAN binding | `unifi_wifi_broadcast` (`network_id`) |
| Stop clients on one SSID from reaching each other | Layer-2 client isolation | `unifi_wifi_broadcast` (`client_isolation_enabled`) |
| Broadcast an SSID from only some APs | Per-AP targeting | `unifi_wifi_broadcast` (`broadcasting_device_filter`) |
| Encryption strength and management-frame protection | Security mode + PMF | `unifi_wifi_broadcast` (`security`, `pmf_mode`) |

-> **Scope note:** The Layer-3 policy that says "the IoT VLAN cannot reach the Trusted VLAN" is a **gateway firewall rule**, not a WiFi setting. It lives on your UniFi gateway (or upstream router) and is out of scope for these resources. See [Inter-VLAN Firewalling Lives on the Gateway](#inter-vlan-firewalling-lives-on-the-gateway) below. Client isolation and inter-VLAN firewalling are different controls, and a real segmentation design uses both.

## Step 1: Define the VLANs

Each `unifi_network` is a VLAN-only network, an 802.1Q tag the SSID hands its clients. Pick a distinct `vlan_id` per device class.

```terraform
resource "unifi_network" "trusted" {
  name    = "Trusted"
  vlan_id = 100
  enabled = true
}

resource "unifi_network" "iot" {
  name    = "IoT"
  vlan_id = 200
  enabled = true
}

resource "unifi_network" "guest" {
  name    = "Guest"
  vlan_id = 300
  enabled = true
}
```

`vlan_id` accepts `1` (the default network) through `4009`. The three IDs above are arbitrary; use whatever your gateway's Layer-3 config expects for each subnet.

## Step 2: Bind an SSID to Each VLAN

An SSID sends its clients into a VLAN through `network_id`. Set it to the `id` of the matching `unifi_network`; omit it and clients land on the native network instead. This binding, not the SSID name, is what actually segments the traffic.

```terraform
# Trusted: modern clients only. WPA3, PMF required, isolation OFF so your
# own devices can reach each other (laptop -> printer, phone -> cast).
resource "unifi_wifi_broadcast" "trusted" {
  name                         = "Corp WiFi"
  security                     = "WPA3_PERSONAL"
  passphrase                   = var.trusted_passphrase
  pmf_mode                     = "REQUIRED"
  broadcasting_frequencies_ghz = ["2.4", "5", "6"]
  network_id                   = unifi_network.trusted.id
  client_isolation_enabled     = false
}

# IoT: legacy gear that can't do WPA3. Isolate every client from its peers,
# so one compromised bulb can't pivot to the next.
resource "unifi_wifi_broadcast" "iot" {
  name                         = "IoT Devices"
  security                     = "WPA2_PERSONAL"
  passphrase                   = var.iot_passphrase
  pmf_mode                     = "OPTIONAL"
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.iot.id
  client_isolation_enabled     = true
}

# Guest: mixed fleet you don't control. WPA2/WPA3 transition, isolated.
resource "unifi_wifi_broadcast" "guest" {
  name                         = "Guest"
  security                     = "WPA2_WPA3_PERSONAL"
  passphrase                   = var.guest_passphrase
  pmf_mode                     = "OPTIONAL"
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.guest.id
  client_isolation_enabled     = true
}
```

Keep passphrases out of your configuration and out of version control:

```terraform
variable "trusted_passphrase" {
  type      = string
  sensitive = true
}

variable "iot_passphrase" {
  type      = string
  sensitive = true
}

variable "guest_passphrase" {
  type      = string
  sensitive = true
}
```

~> **Security note:** `passphrase` is marked sensitive by the provider, so Terraform redacts it from plan output and the CLI. It still lands in state, so treat your state file as a secret: use a remote backend with encryption at rest and restricted access, and supply the values through `TF_VAR_*` environment variables or a secrets manager rather than a committed `.tfvars`.

## Choosing a Security Mode

`security` selects the encryption suite. The right choice trades compatibility against strength:

| Mode | Use when |
|---|---|
| `WPA3_PERSONAL` | Modern, trusted clients only. Strongest option; forces PMF. |
| `WPA2_WPA3_PERSONAL` | Mixed fleet you don't control (guests). Lets WPA3 clients use WPA3 while WPA2-only clients still associate. |
| `WPA2_PERSONAL` | Legacy IoT that cannot speak WPA3 and won't join a transition SSID. |
| `OPEN` | No encryption. Only for a captive-portal SSID paired with a `unifi_hotspot_voucher`; never for a network carrying real traffic. |

The personal modes require a `passphrase` of 8-63 characters. `OPEN` takes no passphrase.

## Protected Management Frames (PMF)

`pmf_mode` controls Protected Management Frames, which guard against deauthentication and disassociation attacks:

- `REQUIRED` rejects any client that can't do PMF. This is the correct setting for a trusted, modern SSID. `WPA3_PERSONAL` implies it regardless.
- `OPTIONAL` lets non-PMF clients associate anyway. Set this on IoT and guest SSIDs, where older chipsets reject `REQUIRED` and silently fail to connect.

If an IoT device joins on the controller but never gets an IP, `pmf_mode = "REQUIRED"` is a common culprit, drop it to `OPTIONAL` for that SSID.

## Client Isolation

`client_isolation_enabled = true` blocks station-to-station traffic **within the same SSID**. A phone and a smart plug on the isolated IoT SSID can each reach the gateway, but not each other. This is the single most effective control for a device class where any member could be compromised.

Guidance by SSID:

- **IoT:** on. Cameras, plugs, and sensors rarely need to talk to each other, and one weak device shouldn't be able to scan its neighbors.
- **Guest:** on. Visitors' devices should never see each other.
- **Trusted:** off. Your own devices need peer reachability (printing, casting, file shares).

-> **Note:** Isolation is a Layer-2 control scoped to one SSID's clients. It does **not** stop traffic from crossing into another VLAN, that's the gateway firewall's job, covered next.

## Targeting Specific Access Points

By default an SSID broadcasts from every AP on the site. To pin an SSID to specific APs, list their UUIDs in `broadcasting_device_filter`. Resolve those UUIDs at plan time with the `unifi_devices` data source instead of hard-coding them:

```terraform
data "unifi_devices" "all" {}

locals {
  # Match one AP by the name shown in the UniFi console.
  lab_ap = one([for d in data.unifi_devices.all.devices : d.id if d.name == "Office AP"])
}

# A trusted SSID that only exists near one AP, e.g. a lab or a back office.
resource "unifi_wifi_broadcast" "lab_only" {
  name                         = "Lab"
  security                     = "WPA3_PERSONAL"
  passphrase                   = var.lab_passphrase
  pmf_mode                     = "REQUIRED"
  broadcasting_frequencies_ghz = ["5", "6"]
  network_id                   = unifi_network.trusted.id
  broadcasting_device_filter   = [local.lab_ap]
}
```

`data.unifi_devices.all.devices` exposes each adopted device's `id`, `name`, `model`, `mac_address`, `ip_address`, and `state`, so you can filter by whichever attribute is stable in your environment. Omit `broadcasting_device_filter` entirely to broadcast everywhere.

## Inter-VLAN Firewalling Lives on the Gateway

This provider manages the wireless and VLAN layer: it creates the VLANs, binds each SSID to one, and isolates clients inside an SSID. It does **not** manage the routing policy between VLANs.

The rules that enforce your segmentation intent at Layer 3, for example:

| From | To | Action |
|---|---|---|
| Guest VLAN | Trusted VLAN | drop |
| IoT VLAN | Trusted VLAN | drop |
| IoT VLAN | Internet | allow |
| Trusted VLAN | IoT VLAN | allow (so you can control your devices) |

...are **gateway firewall rules**, configured on your UniFi gateway or upstream router. Create them there. Without them, the VLANs are separate broadcast domains but still route to one another, and the segmentation is cosmetic. The two controls compose:

- **Client isolation** (this provider) stops peers *within* one VLAN.
- **Firewall rules** (your gateway) stop traffic *between* VLANs.

A complete design uses both.

## Full Worked Example

Putting the pieces together: a provider block, three VLANs, and three segmented SSIDs.

```terraform
terraform {
  required_providers {
    unifi = {
      source = "PjSalty/unifi"
    }
  }
}

provider "unifi" {
  api_url = "https://unifi.example.com" # or UNIFI_API
  api_key = var.unifi_api_key           # or UNIFI_API_KEY
  site    = "default"                   # or UNIFI_SITE
}

# --- VLANs ---------------------------------------------------------------

resource "unifi_network" "trusted" {
  name    = "Trusted"
  vlan_id = 100
}

resource "unifi_network" "iot" {
  name    = "IoT"
  vlan_id = 200
}

resource "unifi_network" "guest" {
  name    = "Guest"
  vlan_id = 300
}

# --- SSIDs ---------------------------------------------------------------

resource "unifi_wifi_broadcast" "trusted" {
  name                         = "Corp WiFi"
  security                     = "WPA3_PERSONAL"
  passphrase                   = var.trusted_passphrase
  pmf_mode                     = "REQUIRED"
  broadcasting_frequencies_ghz = ["2.4", "5", "6"]
  network_id                   = unifi_network.trusted.id
  client_isolation_enabled     = false
}

resource "unifi_wifi_broadcast" "iot" {
  name                         = "IoT Devices"
  security                     = "WPA2_PERSONAL"
  passphrase                   = var.iot_passphrase
  pmf_mode                     = "OPTIONAL"
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.iot.id
  client_isolation_enabled     = true
}

resource "unifi_wifi_broadcast" "guest" {
  name                         = "Guest"
  security                     = "WPA2_WPA3_PERSONAL"
  passphrase                   = var.guest_passphrase
  pmf_mode                     = "OPTIONAL"
  broadcasting_frequencies_ghz = ["2.4", "5"]
  network_id                   = unifi_network.guest.id
  client_isolation_enabled     = true
}
```

Then apply:

```bash
terraform init
terraform plan
terraform apply
```

## Verify

After `terraform apply`, confirm the segmentation took effect:

1. In the UniFi console, open each SSID and check that its network binding points at the expected VLAN and that **Client Device Isolation** is on for IoT and Guest.
2. Join a phone to the IoT SSID and confirm it receives an address on the IoT subnet, not the Trusted one.
3. From that IoT client, try to reach a device on the Trusted VLAN. With the gateway firewall rules in place, it should fail.
4. From the same IoT client, try to reach another device on the IoT SSID. With `client_isolation_enabled = true`, that should also fail.

If step 3 succeeds, your gateway firewall rules are missing or too permissive, revisit [Inter-VLAN Firewalling Lives on the Gateway](#inter-vlan-firewalling-lives-on-the-gateway). If step 2 lands on the wrong subnet, check `network_id` on the SSID.
