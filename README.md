# terraform-provider-keenetic (`nghminh163/keenetic`)

An OpenTofu / Terraform provider for **Keenetic** routers, driving the router's undocumented
**RCI** HTTP API (the same one the web UI uses). Built because no Keenetic provider exists on the
registry.

Status: **segment management working** (`keenetic_segment`). More resources (DHCP pools, static
leases, Wi-Fi) to follow.

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
internal/client/segment.go    segment model + Get/Apply/Delete
internal/provider/provider.go provider schema + configuration
internal/provider/segment_resource.go   keenetic_segment resource (CRUD + import)
examples/segment/main.tf      runnable example
```

## Roadmap

- [x] `keenetic_segment.ipv4.dhcp` — per-segment DHCP pool (range, **DNS servers**, lease).
- [ ] DHCP static leases (`ip dhcp host`).
- [ ] `ipv4.relay_multicast_dns` — mDNS reflector toggle.
- [ ] `wireless { band_2ghz, band_5ghz }` — SSID / security / PSK (sensitive) per band.
- [ ] Segment membership (ports / VLAN / Wi-Fi AP binding) and ACLs.
- [ ] Publish to the OpenTofu registry so CI can `init` it without dev overrides.
