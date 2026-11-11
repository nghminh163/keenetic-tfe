terraform {
  required_providers {
    keenetic = {
      source = "nghminh163/keenetic"
    }
  }
}

provider "keenetic" {
  # endpoint / username / password from KEENETIC_ENDPOINT / KEENETIC_USERNAME / KEENETIC_PASSWORD
}

# A throwaway test segment with its own DHCP pool, on an unused subnet.
resource "keenetic_segment" "test" {
  name        = "Bridge2"
  description = "tf-managed test segment"

  ipv4 = {
    address = "192.168.90.1"
    mask    = "255.255.255.0"

    dhcp = {
      enabled     = true
      range_start = "192.168.90.33"
      range_size  = 120
      dns_servers = ["1.1.1.1", "8.8.8.8"]
      lease_time  = 25200
    }
  }
}
