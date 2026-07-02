# Changelog

All notable changes to this project are documented here. The format follows
Keep a Changelog, and the project adheres to Semantic Versioning. Releases are
batched from `.changelog/unreleased/` by changie.

## [Unreleased]

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
