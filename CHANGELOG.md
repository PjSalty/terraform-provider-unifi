# Changelog

All notable changes to this project are documented here. The format follows
Keep a Changelog, and the project adheres to Semantic Versioning. Releases are
batched from `.changelog/unreleased/` by changie.

## [Unreleased]

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
