# An A record mapping an internal hostname to an IPv4 address.
resource "unifi_dns_policy" "app_a_record" {
  type         = "A_RECORD"
  domain       = "app.example.com"
  ipv4_address = "192.0.2.10"
  ttl_seconds  = 3600
}
