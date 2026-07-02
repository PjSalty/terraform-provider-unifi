# Security Policy

## Supported versions

The latest released minor receives security fixes. Pre-1.0, only the latest tag
is supported.

## Reporting a vulnerability

Report privately through GitHub security advisories:
<https://github.com/PjSalty/terraform-provider-unifi/security/advisories/new>

Do not open a public issue for a security problem. Include the provider version,
the UniFi Network version you saw it on, and reproduction steps.

In scope: provider bugs that leak the API key, mishandle sensitive attributes, or
send unintended writes to the controller. Out of scope: issues in UniFi Network
itself or in `go-unifi` (report those upstream).

## Release verification

Release artifacts are signed. The signing key is published at
[`docs/gpg-public-key.asc`](docs/gpg-public-key.asc) and is also registered
with the Terraform Registry. Verify the checksums signature before use:

```bash
# Import the signing key
gpg --import docs/gpg-public-key.asc
# Confirm the fingerprint
gpg --fingerprint releases@saltstice.com

gpg --verify terraform-provider-unifi_<version>_SHA256SUMS.sig \
            terraform-provider-unifi_<version>_SHA256SUMS
sha256sum -c terraform-provider-unifi_<version>_SHA256SUMS
```
