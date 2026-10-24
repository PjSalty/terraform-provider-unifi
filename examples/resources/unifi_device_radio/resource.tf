# Tune the three radios of one Wi-Fi 7 access point. Each radio is its own
# resource; attributes left out stay as the controller has them.

locals {
  office_ap = "00:00:5e:00:53:01"
}

# 5 GHz: 80 MHz on a fixed non-DFS channel.
resource "unifi_device_radio" "office_5g" {
  mac               = local.office_ap
  frequency_ghz     = "5"
  channel           = "149"
  channel_width_mhz = 80
}

# 6 GHz: the widest channel, placed by the controller.
resource "unifi_device_radio" "office_6g" {
  mac               = local.office_ap
  frequency_ghz     = "6"
  channel           = "auto"
  channel_width_mhz = 320
}

# 2.4 GHz: narrow and quiet, and drop clients weaker than -75 dBm so they
# move to a closer AP.
resource "unifi_device_radio" "office_2g" {
  mac               = local.office_ap
  frequency_ghz     = "2.4"
  channel_width_mhz = 20
  tx_power_mode     = "low"
  min_rssi_enabled  = true
  min_rssi_dbm      = -75
}
