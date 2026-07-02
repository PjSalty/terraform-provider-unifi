# Changelog

All notable changes to this project are documented here. The format follows
Keep a Changelog, and the project adheres to Semantic Versioning. Releases are
batched from `.changelog/unreleased/` by changie.

## [Unreleased]

## [0.1.0] - 2026-07-01

### Added

- Provider scaffolding on the terraform-plugin-framework: provider configuration
  (`api_url`, `api_key`, `site`, `allow_insecure`, `read_only`,
  `destroy_protection`) with argument-then-environment fallback. Release pipeline,
  CI, and docs tooling adapted from the PjSalty/truenas provider.
