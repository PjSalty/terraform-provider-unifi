# Changelog

All notable changes to this project are documented here. The format follows
Keep a Changelog, and the project adheres to Semantic Versioning. Releases are
batched from `.changelog/unreleased/` by changie.

## [Unreleased]

## [0.3.4] - 2026-07-02

### Added

- `unifi_wifi_broadcast`: `band_steering_enabled` (steer dual-band clients to
  5/6 GHz) and `mlo_enabled` (WiFi 7 Multi-Link Operation). Both Optional and
  sent only when set, so an unset attribute leaves the controller's current
  value untouched (no clobber of a UI setting).

## [0.3.3] - 2026-07-02

### Fixed

- `unifi_wifi_broadcast` create/update rejected HTTP 400 "WPA security combined
  with standard WiFi requires fast roaming setting". The security-config object
  never set `fastRoamingEnabled`, so it serialized to null and the controller
  refused every WPA-personal SSID. `expandSecurity` now always sends it (plus
  `wpa3FastRoamingEnabled` on the WPA2/WPA3 mixed variant).

### Added

- `unifi_wifi_broadcast`: `fast_roaming_enabled` (802.11r Fast BSS Transition),
  Optional+Computed, defaults off (802.11r can disrupt some legacy clients).
  Enable it for seamless multi-AP roaming.

## [0.3.2] - 2026-07-02

### Fixed

- `unifi_wifi_broadcast` create and update now succeed against the controller.
  The Integration API rejects any SSID write whose `arpProxyEnabled`,
  `advertiseDeviceName`, or `bssTransitionEnabled` is null (its union marshaler
  emits these keys even when unset), so every create/update returned HTTP 400
  "must not be null". The three are now modeled and always sent.

### Added

- `unifi_wifi_broadcast`: `bss_transition_enabled` (802.11v BSS Transition
  Management, defaults on), `arp_proxy_enabled` (Proxy ARP, defaults off), and
  `advertise_device_name` (defaults off). All Optional+Computed with the UniFi
  defaults, and refreshed on Read from the STANDARD variant for drift detection.

## [0.3.1] - 2026-07-02

### Added

- `unifi_wifi_broadcasts` data source: lists every SSID (id, name, enabled,
  type, origin) so an existing SSID's UUID can be resolved by name, e.g. to
  drive `import` blocks without hand-hunting controller IDs.

## [0.3.0] - 2026-07-02

### Added

- `unifi_firewall_policy` resource: zone-based firewall rules with full CRUD.
  Firewall zones alone are inert, so this adds the rules that govern traffic
  between them. Covers action (ALLOW/BLOCK/REJECT with allow_return_traffic),
  ip_protocol_scope (ip_version plus named protocol), connection_state_filter,
  ipsec_filter, and source/destination blocks (zone_id plus network/ip/port/
  domain traffic filters). Niche filter variants (region, VPN, application,
  ranges) and the schedule block are deferred with greppable `shortcut:` notes.
- Eight read-only reference data sources that resolve names to the UUIDs
  resources need: `unifi_sites`, `unifi_networks`, `unifi_firewall_zones`
  (surfaces SYSTEM-defined zones that no resource can create),
  `unifi_radius_profiles`, `unifi_wans`, `unifi_device_tags`,
  `unifi_dpi_applications`, and `unifi_dpi_application_categories`.
- `unifi_wifi_broadcast`: `multicast_to_unicast_conversion_enabled` (Multicast
  Enhancement / IGMPv3), `uapsd_enabled` (WMM power save), and
  `client_filter_action` plus `client_filter_mac_addresses` (per-SSID MAC
  allow/deny list, e.g. to lock a hardened IoT SSID to a known device
  allowlist). The two toggles also refresh on Read for drift detection.

## [0.2.0] - 2026-07-01

### Changed

- BREAKING: renamed the `unifi_hotspot_voucher` batch-size attribute `count`
  to `quantity`. `count` is a reserved Terraform meta-argument, so the
  attribute was unusable as released in 0.1.0.

### Added

- Full documentation tree: per-resource and per-data-source pages, provider
  index, and guides for getting started, importing an existing controller,
  and network segmentation.
- `internal/testutil`: shared test doubles (generated-mock wiring plus a
  minimal Integration-API httptest server) used to unit-test every layer.
- Unit coverage raised from ~25% to 97-100% per package, enforced by the CI
  coverage gate (floors: resources 97 at its provable ceiling, datasources /
  provider / testutil 100).

### Fixed

- gosec G115/G109 integer-narrowing findings via a saturating `safeInt32`
  helper at every schema-validated conversion site.
- Toolchain pinned to go1.26.4 so builds carry a patched stdlib
  (govulncheck-clean).

## [0.1.0] - 2026-07-01

### Added

- Provider scaffolding on the terraform-plugin-framework: provider configuration
  (`api_url`, `api_key`, `site`, `allow_insecure`, `read_only`,
  `destroy_protection`) with argument-then-environment fallback. Release pipeline,
  CI, and docs tooling adapted from the PjSalty/truenas provider.
