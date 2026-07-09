# SPR Home Assistant Integration

Connects [SPR (Secure Programmable Router)](https://www.supernetworks.org/) to
[Home Assistant](https://www.home-assistant.io/). Two halves, one repo:

- **`code/` + `docker-compose.yml`** — the `ha_sync` SPR plugin. Runs on the
  router, aggregates SPR state (devices, wifi stations, ARP, traffic
  accounting, release info), listens on the sprbus for instant
  connect/disconnect events, and serves a small token-authenticated HTTP API
  on the LAN for Home Assistant. It advertises itself over mDNS
  (`_spr-ha._tcp`) so Home Assistant discovers the router automatically.
- **`custom_components/spr/`** — the Home Assistant custom integration
  (HACS-compatible layout). Zeroconf-discovered config flow, one
  `DataUpdateCoordinator` polling cycle, plus a server-sent-events channel so
  presence updates land within about a second of a device joining or leaving.

## Device sync: SPR as a discovery provider for Home Assistant

Every client on the SPR network becomes a `device_tracker` (ScannerEntity)
registered by MAC address in the Home Assistant device registry, parented to
the router via `via_device`. These entities publish `ip`, `mac`, and
`host_name` attributes with `source_type: router` — exactly what Home
Assistant's DHCP discovery watches
(see [network discovery](https://developers.home-assistant.io/docs/network_discovery/)).
SPR therefore *teaches* Home Assistant what lives on the network: other
integrations (ESPHome devices, TVs, printers, …) get discovered from SPR's
device table without waiting to sniff a DHCP renewal.

## Features

| Platform | Entities |
| --- | --- |
| `device_tracker` | Presence per client (wifi station list + ARP + sprbus events), with a configurable *consider home* grace period for phones that sleep their Wi-Fi |
| `sensor` | WAN download/upload rate, WAN download/upload totals (metered-connection friendly, `total_increasing`), connected client count, WAN IP, boot time, load averages (1/5/15m, disabled by default) |
| `binary_sensor` | WAN connectivity |
| `switch` | Guest Wi-Fi on/off (extra BSS on every AP), per-device internet blocking via SPR's `wan` policy (parental controls; disabled by default to avoid entity explosion) |
| `button` | Restart SPR services; per-device Wake on LAN (disabled by default) |
| `update` | SPR release: installed vs newest published version |
| service | `spr.wake_on_lan` — magic packet from the router itself |

Presence is event-driven where possible: the plugin subscribes to
`wifi:auth:success`, `wifi:station:disconnect`, `dhcp:request` and `device:*`
sprbus topics and pushes transitions to Home Assistant over SSE, so
automations like "turn off the lights when everyone leaves" react quickly.

## Installing the SPR plugin

Via the SPR UI: **System → Plugins → + New Plugin** and point it at this
repository (`GitURL`), or register `plugin.json` manually. The compose file
must be whitelisted in `configs/base/custom_compose_paths.json`:

```sh
cat configs/base/custom_compose_paths.json | \
  jq '. + ["plugins/home_assistant_integration/docker-compose.yml"]'
```

SPR writes a scoped API token (see `ScopedPaths` in `plugin.json` — the
plugin can read state and edit devices/wifi, nothing else) to
`configs/plugins/home_assistant/api-token`. On first start the plugin
generates:

- a **pairing token** Home Assistant must present (`HAToken`), and
- a stable `RouterID` used by discovery.

Get the pairing token from the plugin API (or the SPR UI plugin page):

```sh
curl --unix-socket state/plugins/home_assistant/socket http://localhost/config
# rotate it any time:
curl -X PUT --unix-socket state/plugins/home_assistant/socket http://localhost/token/rotate
```

## Installing the Home Assistant integration

- **HACS**: add this repository as a custom repository (type: integration),
  install "SPR (Secure Programmable Router)".
- **Manual**: copy `custom_components/spr/` into your Home Assistant
  `config/custom_components/`.

Restart Home Assistant. If HA and SPR share a broadcast domain the router is
discovered automatically (`_spr-ha._tcp` via zeroconf) — enter the pairing
token when prompted. Otherwise add it via **Settings → Devices & Services →
Add Integration → SPR** with host, port (default `8321`) and token.

Options (⚙ on the integration): *consider home* seconds, and whether new
devices start with tracking enabled.

## Reproducible builds (supply-chain security)

The plugin image builds bit-for-bit reproducibly, following the same scheme
as the other SPR extensions:

- every base image (`ubuntu`, `alpine`, `container_template`, the BuildKit
  builder and the Dockerfile syntax sled) is pinned by digest in
  `reproducible.env`,
- apt installs resolve against a frozen `snapshot.ubuntu.com` timestamp,
- the Go toolchain is fetched by version with per-arch SHA256 verification
  and `GOTOOLCHAIN=local`,
- `SOURCE_DATE_EPOCH=0` plus BuildKit's `rewrite-timestamp=true` exporter
  normalize all file timestamps, and file modes are normalized before COPY.

Build locally:

```sh
./build_docker_compose.sh
```

Re-pin inputs (review with `git diff` afterwards):

```sh
./update-pins.sh
```

CI (`.github/workflows/docker-image.yml`) builds multi-arch images the same
way, signs them with cosign (keyless, GitHub OIDC) and attaches SLSA
provenance attestations, then verifies both. `validate.yml` runs the Go unit
tests and Home Assistant's hassfest on every push.

Run the unit tests in a container:

```sh
./test-unit.sh
```

## Security model

- The LAN API is bearer-token authenticated (constant-time compare); only
  `/api/probe` (product name, version, router id — no device data) is open,
  because the HA config flow needs to identify the router before pairing.
- The plugin's SPR API token is scoped (`ScopedPaths`) to the endpoints it
  needs; most are read-only (`:r`).
- Rotating the pairing token from the SPR side immediately locks out Home
  Assistant, which then prompts for re-authentication.
- No cloud, no outbound connections: everything stays on your LAN. The only
  remote call is SPR's own registry query for available release tags.
