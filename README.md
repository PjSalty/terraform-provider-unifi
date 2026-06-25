# terraform-provider-unifi

Terraform provider for the **official** UniFi Network Integration API.

It manages a self-hosted UniFi Network controller (UniFi OS, Network >= 10.1.78)
through the official Integration API (`/proxy/network/integration/v1/`) with an
`X-API-KEY`. Built on the terraform-plugin-framework over the
`github.com/filipowm/go-unifi/v2` official client.

> Status: in development. The provider configuration is in place; resources land
> incrementally (see CHANGELOG). Requires a UniFi OS console (UDM, Cloud Key, or
> UniFi OS Server). The legacy self-hosted Network Application container is not
> supported, it has no API keys.

## Why this exists

No released Terraform provider consumes the official Integration API.
`filipowm/unifi` (v1.0.0 and main) and `ubiquiti-community/unifi` both ride the
legacy reverse-engineered API (`go-unifi` v1.x, whose schema substrate is capped
at controller 9.5.21). This provider targets the official, OpenAPI-3.1-specced
surface via `go-unifi/v2`.

## Provider configuration

```hcl
provider "unifi" {
  api_url        = "https://unifi.example.com" # https only;        env UNIFI_API
  api_key        = var.unifi_api_key           # X-API-KEY;         env UNIFI_API_KEY
  site           = "default"                   #                    env UNIFI_SITE
  allow_insecure = true                        # self-signed cert;  env UNIFI_INSECURE
}
```

Generate the API key in the controller UI under **Settings > Control Plane >
Integrations**. Username/password is not supported, the official API requires an
API key.

## Coverage

Targets the full official surface: networks, wifi broadcasts, devices, clients,
ACLs, DNS policies, firewall, hotspot, traffic-matching lists, sites. Implemented
first: `unifi_network`, `unifi_wifi_broadcast`, and a `unifi_devices` data source.

## Development

```bash
make build   # compile
make test    # unit tests, no controller needed
make lint    # golangci-lint
make docs    # regenerate docs
```

## License

MPL-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
