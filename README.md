# terraform-provider-keenetic (`nghminh163/keenetic`)

An OpenTofu / Terraform provider for **Keenetic** routers, driving the router's undocumented
**RCI** HTTP API (the same one the web UI uses). Built because no Keenetic provider exists on the
registry.

Status: **working.** `keenetic_segment` (IPv4 + DHCP pool + Wi-Fi + VLAN, with VLAN-conflict
detection), `keenetic_static_lease` (DHCP reservations) and `keenetic_mdns_reflector` (global mDNS).
Verified end-to-end against a live router.

## Authentication

Keenetic's NDM challenge-response, implemented in `internal/client`:

```
GET  /auth                          -> 401 + headers X-NDM-Realm, X-NDM-Challenge (+ session cookie)
md5  = MD5(username ":" realm ":" password)
sha  = SHA256(challenge + md5)
POST /auth {login, password: sha}   -> 200, session cookie now authenticated
```

Writes POST a command array to `/rci/` and are persisted with `system configuration save`.

## Usage

```hcl
terraform {
  required_providers {
    keenetic = { source = "nghminh163/keenetic" }
  }
}

provider "keenetic" {
  endpoint = "http://192.168.18.1" # or env KEENETIC_ENDPOINT
  username = "admin"               # or env KEENETIC_USERNAME
  password = "..."                 # or env KEENETIC_PASSWORD (sensitive)
}

resource "keenetic_segment" "test" {
  name        = "Bridge2"        # interface id; changing it replaces the segment
  description = "my segment"

  ipv4 = {
    address = "192.168.90.1"     # segment gateway IPv4
    mask    = "255.255.255.0"    # optional, default /24

    dhcp = {                     # optional: omit to run the segment without DHCP
      enabled     = true
      range_start = "192.168.90.33"
      range_size  = 120                       # last address derived into range_end
      dns_servers = ["45.90.28.137", "45.90.30.137"]
      lease_time  = 25200
    }
  }

  # security_level = "private"   # private | protected | public (default private)
  # up             = true        # default true

  # Optional 802.1Q VLAN: one id tagged on physical ports (each -> a <port>/Vlan<id> sub-interface
  # included in the bridge). Applying an id another segment already carries fails with a conflict.
  vlan = {
    id    = 89
    ports = ["GigabitEthernet0", "GigabitEthernet1"]
  }

  # Optional Wi-Fi: up to one AP per band, each on a spare WifiMaster<n>/AccessPoint<m> slot
  # (slots 0/1 are the router's own networks). The AP is configured and included in the bridge.
  wireless = {
    band_2ghz = {
      access_point = "WifiMaster0/AccessPoint2" # 2.4 GHz -> WifiMaster0/*
      ssid         = "my-24"
      psk          = var.wifi_psk               # sensitive; keep out of committed config
      # security   = "wpa2-psk"                 # wpa2-psk | wpa-wpa2-psk | wpa-psk | open
      # enabled    = true                       # false keeps the AP configured but down
    }
    band_5ghz = {
      access_point = "WifiMaster1/AccessPoint2" # 5 GHz -> WifiMaster1/*
      ssid         = "my-5"
      psk          = var.wifi_psk
    }
  }
}

# DHCP static reservation (global `ip dhcp host`; owns only the MAC it declares).
resource "keenetic_static_lease" "nas" {
  mac = "aa:bb:cc:dd:ee:ff"
  ip  = "192.168.90.10"
}

# Global mDNS reflector (relays Bonjour/Chromecast between segments). One per router.
resource "keenetic_mdns_reflector" "this" {
  enforce = true
}
```

A `keenetic_segment` is an `interface Bridge<N>` with IPv4 settings and an optional bound DHCP pool
(named `tf-<segment>`, so it never collides with the router's own `_WEBADMIN` pool). It is created
with **no member ports, VLANs or Wi-Fi APs** — attach those in the UI or via future resources. Import
an existing one with `tofu import keenetic_segment.x Bridge1`.

## Build & local test (dev overrides)

No registry publish needed to test locally:

```bash
go build -o terraform-provider-keenetic .

cat > dev.tfrc <<'EOF'
provider_installation {
  dev_overrides { "nghminh163/keenetic" = "/absolute/path/to/this/repo" }
  direct {}
}
EOF

cd examples/segment
export KEENETIC_ENDPOINT=http://192.168.18.1 KEENETIC_USERNAME=admin KEENETIC_PASSWORD=...
export TF_CLI_CONFIG_FILE=../../dev.tfrc   # with dev_overrides you skip `tofu init`
tofu apply      # creates Bridge2
tofu destroy    # removes it
```

Verified end-to-end against a Keenetic Xiaomi 3P (firmware 5.0.12): apply creates the Bridge
interface, a re-plan is clean, destroy removes it (confirmed directly over RCI).

## Layout

```
main.go                       provider server entry (address registry.terraform.io/nghminh163/keenetic)
internal/client/client.go     RCI client: NDM auth, GET/POST, config save, error parsing
internal/client/segment.go    segment model + Get/Apply/Delete (orchestrates DHCP + Wi-Fi)
internal/client/wireless.go   access-point config + bridge membership (per-band)
internal/client/vlan.go       VLAN sub-interfaces + bridge membership + conflict detection
internal/client/static_lease.go  ip dhcp host get/apply/delete
internal/client/mdns.go       global mDNS reflector get/set
internal/provider/provider.go provider schema + configuration (registers all resources)
internal/provider/segment_resource.go       keenetic_segment (CRUD + import)
internal/provider/static_lease_resource.go  keenetic_static_lease (CRUD + import)
internal/provider/mdns_resource.go          keenetic_mdns_reflector (CRUD)
examples/segment/main.tf      runnable example
```

## Roadmap

- [x] `keenetic_segment.ipv4.dhcp` — per-segment DHCP pool (range, **DNS servers**, lease).
- [x] `keenetic_static_lease` — DHCP reservations (`ip dhcp host`), keyed per MAC.
- [x] `keenetic_mdns_reflector` — global mDNS reflector (it is router-wide in RCI, so a resource of its
  own rather than a per-segment field).
- [x] `keenetic_segment.wireless { band_2ghz, band_5ghz }` — SSID / security / PSK (sensitive) / enabled
  per band, on a spare AP slot, bound to the segment's bridge.
- [x] `keenetic_segment.vlan { id, ports }` — 802.1Q VLAN sub-interfaces bound to the segment, with
  conflict detection (refuses a VLAN id already carried by another segment).
- [ ] Switch-port / `iseg` port assignment (tagged vs untagged ports) and ACLs.
- [ ] Publish to the OpenTofu registry so CI can `init` it without dev overrides / a built mirror.
