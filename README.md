# helianthus-ebus-vdev

`helianthus-ebus-vdev` is a virtual eBUS device emulator for the Helianthus platform. It emulates slave devices on the eBUS network, allowing non-Vaillant data sources (Home Assistant sensors, Matter thermostats, etc.) to appear as native Vaillant peripherals to a VRC700 system controller.

## Purpose and Scope

### What belongs in this repo

- Virtual device implementations (`internal/device/thermostat/`) — currently VR90 room controller.
- Slave wire FSM and frame parsing (`internal/bus/`) — passive listen, ACK/NACK, response encoding, collision detection.
- Data source adapters (`internal/datasource/`) — GraphQL zone subscription from the Helianthus gateway.
- Configuration and binary entrypoint (`internal/config/`, `cmd/vdev/`).

### What does not belong in this repo

- Low-level transport framing and bus primitives (use `helianthus-ebusgo`).
- Registry/provider model or schema definitions (use `helianthus-ebusreg`).
- Gateway runtime, GraphQL API surfaces, or master-side bus operations (use `helianthus-ebusgateway`).

## How It Works

```text
  Gateway (master)              eBUS Network
┌──────────────────┐                 │
│ GraphQL API      │                 │
│ zoneUpdate sub   │──── master ─────┤
└──────┬───────────┘                 │
       │ WebSocket subscription      │
       ▼                             │
  helianthus-ebus-vdev               │
┌──────────────────┐                 │
│ VR90 @ 0x75      │──── slave ──────┤
│ reports Zone2    │                 │
│ temp as Zone3    │                 │
└──────────────────┘                 │
                                     │
                              VRC700 (0x10)
                              polls emulated VR90
```

The emulator connects to the eBUS adapter as a passive listener. When the VRC700 polls a slave address owned by a virtual device, the emulator responds with properly encoded eBUS slave responses — including identification, scan ID chunks, and live sensor data from the gateway's GraphQL subscription.

## Supported Devices

| Device | Type String | Protocol | Description |
|--------|-------------|----------|-------------|
| VR90   | `thermostat/vr90` | B509 (0xB5/0x09) | Room controller with temperature reporting |

### VR90 Features

- Identifies as Vaillant `RC C` (MF=0xB5, SW=0508, HW=6201)
- Reports live temperature from a configurable gateway zone via GraphQL subscription
- Slow-drift jitter: random walk bounded by configurable range (default ±0.5°C)
- Sensor fault fallback when data source is unavailable
- Accepts B509 write commands silently

## Configuration

```yaml
adapter:
  protocol: enh          # "enh" or "tcp-plain"
  address: "192.168.100.2:9999"

gateway:
  graphql_url: "ws://localhost:8080/graphql"

devices:
  - type: thermostat/vr90
    name: "Zone3 Virtual Thermostat"
    source_zone: "zone-2"
    preferred_address: 0x70       # RCC master address
    temp_jitter_c: 0.5            # ±°C jitter bound (default: 0.5)
    response_delay_ms: 8          # slave response delay (default: 8)
    scan_id: ""                   # optional scan ID string
    force_address_conflict: false  # ignore address collisions
```

### Valid RCC Addresses

Master/slave pairs for room controllers:

| Master | Slave |
|--------|-------|
| 0x17   | 0x1C  |
| 0x30   | 0x35  |
| 0x37   | 0x3C  |
| 0x70   | 0x75  |
| 0x77   | 0x7C  |
| 0xF0   | 0xF5  |
| 0xF7   | 0xFC  |

## Quickstart

### Build

```bash
cd helianthus-ebus-vdev
go build ./cmd/vdev/
```

### Cross-compile for Home Assistant (RPi4)

```bash
GOOS=linux GOARCH=arm64 go build -o vdev-arm64 ./cmd/vdev/
```

### Run

```bash
./vdev -config config.yaml
```

### Run tests

```bash
go test ./...
go test -race -count=1 ./...
```

## Startup Sequence

1. Load and validate YAML config
2. Dial eBUS adapter (ENH or TCP-plain transport)
3. Preflight listen (3s) — detect slave address conflicts on the bus
4. Build emulation targets for each configured device
5. Start GraphQL zone subscriptions (one per device)
6. Run slave responder — listen for polls, respond as configured devices
7. Graceful shutdown on SIGINT/SIGTERM

## Helianthus Dependency Chain

```text
helianthus-ebusgo  ->  helianthus-ebus-vdev
 (transport/proto)      (virtual device emulation)

helianthus-ebusgo  ->  helianthus-ebusreg  ->  helianthus-ebusgateway
 (transport/proto)     (registry/schema)        (runtime/API)
```

`helianthus-ebus-vdev` depends only on `helianthus-ebusgo` for transport, protocol, emulation, and type codecs. It has no dependency on `helianthus-ebusreg` or `helianthus-ebusgateway`.

## Project Structure

```
helianthus-ebus-vdev/
├── cmd/vdev/main.go                      # binary entrypoint
├── internal/
│   ├── bus/
│   │   ├── frame_reader.go               # SYN-delimited frame parser
│   │   ├── slave_responder.go            # slave wire FSM
│   │   └── slave_responder_test.go
│   ├── config/
│   │   ├── config.go                     # YAML config + RCC validation
│   │   └── config_test.go
│   ├── datasource/
│   │   ├── source.go                     # ZoneSource interface
│   │   ├── graphql_zone.go               # GraphQL subscription client
│   │   └── graphql_zone_test.go
│   └── device/
│       ├── device.go                     # Device interface
│       ├── registry.go                   # thread-safe device registry
│       ├── registry_test.go
│       └── thermostat/
│           ├── vr90.go                   # VR90 device implementation
│           └── vr90_test.go
├── go.mod
└── go.sum
```

## Link Map

- `helianthus-ebusgo`: https://github.com/d3vi1/helianthus-ebusgo
- `helianthus-ebusgateway`: https://github.com/d3vi1/helianthus-ebusgateway
- VR90 emulation protocol docs: https://github.com/d3vi1/helianthus-docs-ebus/blob/main/protocols/ebus-vaillant-vr90-emulation.md
- eBUS overview: https://github.com/d3vi1/helianthus-docs-ebus/blob/main/protocols/ebus-overview.md
- Issue tracker: https://github.com/d3vi1/helianthus-ebus-vdev/issues

## License

GNU Affero General Public License v3.0 — see [LICENSE](LICENSE).
