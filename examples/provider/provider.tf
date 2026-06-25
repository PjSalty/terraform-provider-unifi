terraform {
  required_providers {
    unifi = {
      source = "PjSalty/unifi"
    }
  }
}

provider "unifi" {
  api_url        = "https://unifi.example.com" # or UNIFI_API
  api_key        = var.unifi_api_key           # or UNIFI_API_KEY
  site           = "default"                   # or UNIFI_SITE
  allow_insecure = true                        # self-signed console cert; or UNIFI_INSECURE
}
