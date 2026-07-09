# LLM Autocoded SPR Home Assistant Integration

## NOTE: This was autocoded with LLMs


Connects [SPR (Secure Programmable Router)](https://www.supernetworks.org/) to
[Home Assistant](https://www.home-assistant.io/). **Read-only by design**: the
integration observes the network; all control stays in the SPR UI.

Two halves, one repo:

- **`code/` + `docker-compose.yml`** — the `ha_sync` SPR plugin. Runs on the
  router, aggregates SPR state (presence via SPR's `/topology`, traffic
  accounting, uptime/load, release info), listens on the sprbus for instant
  connect/disconnect events, and serves a small **read-only** API on its
  plugin unix socket. It opens **no TCP ports**: Home Assistant reaches it
  through SPR's existing authenticated API proxy at
  `/plugins/home_assistant/ha/v1/*`.
- **`custom_components/spr/`** — the Home Assistant custom integration
  (HACS-compatible layout). One `DataUpdateCoordinator` polling cycle plus a
  server-sent-events stream, so presence lands within about a second. It
  issues **GET requests only** and holds a token that cannot write.

## Architecture and security model

```
Home Assistant ──HTTPS──> SPR API (443/80) ──unix socket──> ha_sync plugin
   GET only          scoped token             (read-only routes)
                     /plugins/home_assistant/:r
                                                ha_sync ──> SPR API (gateway:80)
                                                            read-only install token
                                                ha_sync <── sprbus events (unix)
```

- The plugin's only listener is its unix socket; SPR authenticates every
  caller before proxying, and strips credentials so the plugin never sees
  them. The plugin's route table is GET-only (anything else is 405).
- Home Assistant authenticates with an SPR API token **scoped to
  `/plugins/home_assistant/:r`** — the `:r` suffix makes it GET-only at
  SPR's auth layer, and the path scope means it can reach nothing but this
  plugin's read-only API. Two independent layers enforce read-only.
- The plugin talks to the SPR API with its install token, whose
  `ScopedPaths` in `plugin.json` are all `:r` (read-only). Even a full
  compromise of the plugin process cannot mutate router state.
- The plugin finds the SPR API at the container's default gateway (or
  `127.0.0.1` when `VIRTUAL_SPR=1` puts it in the `service:base`
  namespace); `SPR_API_BASE` overrides for tests.

### Discovery

Home Assistant can surface the router automatically. The plugin advertises a
DNS-SD beacon (`_spr-ha._tcp`) over mDNS, and serves a small **static
identify document** — `{product, id, name}`, no version, no device data — on
SPR's *unauthenticated* public static path
(`/admin/custom_plugin/home_assistant/static/discovery.json`). That path is
gated on `HasUI: true` and, by SPR's design, can only ever reach the
plugin's `/static/*` — the sensitive `/ha/v1/*` routes stay behind the token.

The discovery step reads that document only to present the router and dedup
on its id; it **never rewrites a configured entry's URL from a broadcast**,
so a forged advertisement cannot redirect a router's token to an attacker.
The user still supplies the token by hand. Manual setup (URL + token)
remains available and needs no discovery.

## Device sync: SPR as a discovery provider for Home Assistant

Every client on the SPR network becomes a `device_tracker` (ScannerEntity)
keyed by MAC address. These entities publish `ip`, `mac`, and `host_name`
attributes with `source_type: router` — exactly what Home Assistant's DHCP
discovery watches
(see [network discovery](https://developers.home-assistant.io/docs/network_discovery/)).
SPR therefore *teaches* Home Assistant what lives on the network: other
integrations (ESPHome devices, TVs, printers, …) get discovered from SPR's
device table without waiting to sniff a DHCP renewal.

## Entities

| Platform | Entities |
| --- | --- |
| `device_tracker` | Presence per client (SPR `/topology` + sprbus events), with a configurable *consider home* grace period for phones that sleep their Wi-Fi |
| `sensor` | WAN download/upload rate, WAN download/upload totals (`total_increasing`, metered-connection friendly), connected client count, WAN IP, boot time, load averages (1/5/15m, disabled by default) |
| `binary_sensor` | WAN connectivity |
| `button` | Per-device Wake on LAN (disabled by default; also as the `spr.wake_on_lan` service). The router emits the magic packet itself — exposed as a GET so it works within the read-only token scope, and no SPR API is written |
| `update` | SPR release: installed vs newest published version (read-only; install updates from the SPR UI) |

Device attributes additionally expose wired/wifi, interface, signal, guest
flag, and whether the device's internet is blocked — usable in automations
as read-only signals.

## Installing the SPR plugin

Via the SPR UI: **System → Plugins → + New Plugin** and point it at this
repository (`GitURL`), or register `plugin.json` manually. The compose file
must be whitelisted in `configs/base/custom_compose_paths.json`:

```sh
cat configs/base/custom_compose_paths.json | \
  jq '. + ["plugins/home_assistant_integration/docker-compose.yml"]'
```

SPR writes the plugin's read-only install token to
`configs/plugins/home_assistant/api-token`.

## Installing the Home Assistant integration

1. In the SPR UI, create an API token for Home Assistant with the scoped
   path **`/plugins/home_assistant/:r`** (Auth → API Tokens). The `:r`
   scope makes the token GET-only.
2. Install the integration:
   - **HACS**: add this repository as a custom repository (type:
     integration), install "SPR (Secure Programmable Router)".
   - **Manual**: copy `custom_components/spr/` into your Home Assistant
     `config/custom_components/`.
3. Restart Home Assistant, then **Settings → Devices & Services → Add
   Integration → SPR**. Enter the router URL (e.g. `https://192.168.2.1`)
   and the token. Leave *Verify SSL* off for SPR's self-signed certificate,
   or on if you've installed a proper one.

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
provenance attestations, then verifies both. GitHub Actions are SHA-pinned
where third-party. `validate.yml` runs the Go unit tests, Home Assistant's
hassfest, and the HA integration tests on every push.

Run the plugin unit tests in a container:

```sh
./test-unit.sh
```
