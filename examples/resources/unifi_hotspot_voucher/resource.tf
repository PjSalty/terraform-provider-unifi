# A batch of 10 guest WiFi vouchers, each valid for 24 hours (1440 minutes).
resource "unifi_hotspot_voucher" "guest_day_pass" {
  name               = "Guest day pass"
  quantity           = 10
  time_limit_minutes = 1440

  # Optional per-voucher limits; omit any for unlimited.
  data_usage_limit_mbytes = 5120
  rx_rate_limit_kbps      = 50000
  tx_rate_limit_kbps      = 10000
}
