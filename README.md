# broadlink-to-mqtt-gw — Broadlink IR remote to MQTT bridge (Go)

A small, static Go bridge that connects a Broadlink RM mini 3 (and other RM
IR remotes) to MQTT. Learned IR codes are stored under
readable names; publishing a name to `{prefix}/{name}/set` sends the code.
Home Assistant MQTT auto-discovery creates one button per command. A built-in
web UI (React) is used to learn, test, rename and delete the keys.

Structure, config format, MQTT plumbing (`mqtt-gateway`) and logging follow
the sibling bridge `hs100-to-mqtt-gw`.

## Supported devices

IR remotes speaking the classic Broadlink LAN protocol (UDP :80): RM mini 3
(old and new firmware), RM mini, RM4 mini / pro, RM pro (IR only).

**Non-goals:** RF (433 MHz) learning, RM4 temperature/humidity sensors,
auto-discovery (the device IP is static in the config).

## Getting the device onto the WiFi

The USB port of an RM mini 3 is power only. Provisioning goes through its
setup access point:

1. Hold the reset button until the LED blinks (AP mode).
2. Connect the computer to the open WiFi `BroadlinkProv`.
3. `python3 -m venv .venv && .venv/bin/pip install broadlink`, then
   `.venv/bin/python wifi_setup.py "<SSID>" "<password>"` (sends the
   credentials through the WiFi interface, `IFACE` in the script).
4. Switch back to your normal network. Give the device a DHCP reservation.

The device only supports **2.4 GHz** and **WPA2** (no WPA3).

## MQTT topic layout

`{prefix}` defaults to `broadlink` (`mqtt.topic`), `{name}` is the alias from
`devices[]`.

| Topic                       | Direction | Retained | Description                                       |
|-----------------------------|-----------|----------|---------------------------------------------------|
| `{prefix}/bridge/state`     | publish   | yes      | `online` / `offline` (LWT)                        |
| `{prefix}/{name}/available` | publish   | yes      | `online` / `offline` — device reachability        |
| `{prefix}/{name}/state`     | publish   | yes      | JSON state (see below)                            |
| `{prefix}/{name}/set`       | subscribe | —        | Send an IR command                                |
| `{prefix}/{name}/learn`     | subscribe | —        | Start an IR learning session                      |
| `{prefix}/{name}/learned`   | publish   | config   | Result of a learning session                      |
| `{prefix}/{name}/get`       | subscribe | —        | Re-publish the cached `/state`                    |

State payload:

```json
{"locked": false, "last_command": "tv_power", "last_sent": "2026-11-16T17:00:00Z"}
```

### `/set` payloads

| Form              | Example                                  |
|-------------------|------------------------------------------|
| Command name      | `tv_power` (also as JSON string)         |
| Object            | `{"command": "vol_up", "repeat": 3}`     |
| Raw base64 code   | `{"code": "JgBQAAAB..."}`                |

`repeat` is 1–10 (default 1). Unknown commands and invalid payloads are
logged and dropped. Commands are ignored while a learning session runs.

### Learning codes

Publish anything to `{prefix}/{name}/learn` (optionally a command name as a
label), then press the button on the original remote within
`learn-timeout-seconds` (point it at the RM, a few cm away). The result is
logged and published to `{prefix}/{name}/learned`:

```json
{"code": "JgBQAAABKZIT...", "command": "tv_power", "learned_at": "2026-11-16T17:00:00Z"}
```

Copy `code` into `devices[].commands` of the config and restart the bridge.
On failure the payload is `{"error": "..."}` (not retained).
Example: `mosquitto_pub -t broadlink/rm-mini/learn -m tv_power`.

## Web UI

Served by the bridge itself (default `http://<host>:8080`, see `ui.listen`):

- **Learn:** enter a name (optional), press *Anlernen*, press the key on the
  original remote. The captured signal is drawn as a pulse waveform; *Testen*
  sends it, *Speichern* stores it under the name.
- **Keys:** a tap sends the key (1–3 repetitions selectable). Each key shows
  the waveform of its code.
- **Edit mode:** rename, delete (two-step confirm) and copy the base64 code.
- Keys from the config file are shown read-only; UI keys live in `data-file`
  (`commands.json`) and are merged with them. Changes show up in Home Assistant
  immediately (buttons are added, renamed and removed).

The UI talks to a small JSON API (`/api/devices`, `/api/devices/{d}/learn`,
`/api/devices/{d}/commands/{name}[/send|/rename]`).

**There is no login.** Anyone who can reach the port can send IR commands.
Keep it on a trusted LAN or behind a reverse proxy with authentication. State
changing requests require `Content-Type: application/json`, and no CORS
headers are sent, so other websites cannot trigger commands via your browser.
Set `"ui": {"enabled": false}` to switch it off.

## Home Assistant auto-discovery

On the first successful poll the bridge announces, grouped under one device:
one **button** per configured command (`homeassistant/button/broadlink_{name}_{command}/config`)
and a **Learn** button (entity category *config*). Entities are removed on
graceful shutdown.

## Configuration

One command-line argument: the path to the JSON config. See
[`production/config/config-example.json`](production/config/config-example.json)
(the code in it is only a placeholder — learn your own).

| Field                        | Default     | Description                                              |
|------------------------------|-------------|----------------------------------------------------------|
| `mqtt.url`                   | —           | Required, e.g. `tcp://host:1883`                         |
| `mqtt.username` / `password` | `""`        | Optional; `${ENV}` substitution supported                |
| `mqtt.topic`                 | `broadlink` | Base topic                                               |
| `mqtt.retain` / `qos`        | `true` / `1`|                                                          |
| `devices[].host`             | —           | Required. IP of the remote (UDP/80 must be reachable)    |
| `devices[].name`             | —           | Required. Unique, no `/`, `#`, `+`                       |
| `devices[].commands`         | `{}`        | `name → base64 IR code`; names `[A-Za-z0-9_-]`, not `learn` |
| `data-file`                  | `commands.json` next to the config | Where UI-managed keys are stored; **must be writable** (Docker: `/data/commands.json`) |
| `ui.enabled` / `ui.listen`   | `true` / `:8080` | Web UI and API                                      |
| `polling-interval-seconds`   | `30`        | Health-check interval                                    |
| `learn-timeout-seconds`      | `30`        | How long a learning session waits                        |
| `loglevel`                   | `info`      | `debug`, `info`, `warn`, `error`                         |

## Build and test

Requires Go 1.25+ and Node 22+.

```bash
cd app
make build            # builds the UI (../ui) and embeds it into ./bl2mqtt
make test             # Go tests (-race)
(cd ../ui && npm test)  # UI tests
./bl2mqtt ../production/config/config-example.json

make dev              # UI + API with a simulated remote, no MQTT: http://127.0.0.1:8081
                      # hot reload: `npm run dev` in ../ui (proxies /api to :8081)

# against a real device (hello + auth + update; BROADLINK_LEARN=1 also learns and sends one code)
BROADLINK_HOST=10.0.10.211 go test ./broadlink -run RealDevice -v
```

Without `make ui` the binary still works, but the UI port answers 503
("UI not built"); the MQTT side is unaffected.

## Docker

```bash
docker build -t broadlink-to-mqtt-gw .   # multi-stage: UI, Go binary, distroless image
```

Mount the config read-only and a volume at `/data` for the learned keys; see
`production/docker-compose-bl2mqtt.yaml`. pprof/expvar listen on `:6060`.

## CI

`.github/workflows/ci.yml` runs on every push / pull request: UI tests,
typecheck and build; then Go vet, tests (`-race`), a build with the embedded
UI and golangci-lint; finally a Docker image build (no push).
