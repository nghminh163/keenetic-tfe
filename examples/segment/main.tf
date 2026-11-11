terraform {
  required_providers {
    keenetic = {
      source = "nghminh163/keenetic"
    }
  }
}

provider "keenetic" {
  # endpoint / username / password come from KEENETIC_ENDPOINT, KEENETIC_USERNAME,
  # KEENETIC_PASSWORD so no secrets live in the config.
}

# A throwaway test segment: a Bridge interface with a gateway IP, no ports/VLAN/Wi-Fi attached.
resource "keenetic_segment" "test" {
  name        = "Bridge2"
  description = "tf-managed test segment"
  address     = "192.168.90.1"
  mask        = "255.255.255.0"
  # security_level = "private"  # default
  # up             = true       # default
}
