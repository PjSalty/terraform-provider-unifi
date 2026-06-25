# Contributing

## Layout

- `internal/provider/` -- provider definition and configuration.
- `internal/resources/`, `internal/datasources/` -- resources and data sources,
  added one per change.
- Substrate: `github.com/filipowm/go-unifi/v2`, the official-API client. We write
  resource glue, not API calls.

## Build and test

```bash
make build
make test    # unit tests, no controller needed
make lint
make docs
```

## Acceptance tests

Acceptance tests run against a real UniFi OS console (Network >= 10.1.78) and are
gated on `TF_ACC=1`:

```bash
export UNIFI_API=https://unifi.example.com
export UNIFI_API_KEY=...
export UNIFI_INSECURE=1   # self-signed console cert
make testacc
```

Never run acceptance tests against a production controller.

## Adding a resource

Implement against the `go-unifi/v2` official client (`c.Official()`), map the
discriminated-union request bodies with the generated `From*` helpers, add unit
tests against the generated `*ClientMock`, then an example under `examples/` and
generated docs under `docs/`.
