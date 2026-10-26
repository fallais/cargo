<p align="center">
  <img src="assets/logo.png" alt="cargo" width="360">
</p>

<h1 align="center">cargo</h1>

<p align="center">
  A command-line OBD-II tool. Reads live data and diagnostic trouble codes
  <br>from a vehicle through an ELM327 adapter.
</p>

<p align="center">
  <a href="https://github.com/fallais/cargo/actions/workflows/ci.yml">
    <img alt="CI" src="https://img.shields.io/github/actions/workflow/status/fallais/cargo/ci.yml?branch=main&style=flat-square&logo=github&label=CI">
  </a>
  <a href="https://github.com/fallais/cargo/releases">
    <img alt="Release" src="https://img.shields.io/github/v/release/fallais/cargo?style=flat-square&logo=github&color=blue">
  </a>
  <a href="https://pkg.go.dev/github.com/fallais/cargo">
    <img alt="Go reference" src="https://pkg.go.dev/badge/github.com/fallais/cargo.svg?style=flat-square">
  </a>
  <a href="https://goreportcard.com/report/github.com/fallais/cargo">
    <img alt="Go report card" src="https://goreportcard.com/badge/github.com/fallais/cargo?style=flat-square">
  </a>
  <a href="go.mod">
    <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/fallais/cargo?style=flat-square&logo=go&logoColor=white">
  </a>
  <a href="LICENSE">
    <img alt="Licence" src="https://img.shields.io/github/license/fallais/cargo?style=flat-square&color=green">
  </a>
  <img alt="Platforms" src="https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey?style=flat-square">
  <img alt="Trouble codes" src="https://img.shields.io/badge/trouble%20codes-17%2C958-orange?style=flat-square">
</p>

## Screenshot

<p align="center">
  <img src="assets/screenshot.png" alt="Trouble codes across five modules" width="880">
</p>

Codes grouped by the ECU that reported them. The chassis and airbag faults came
over UDS, so they carry a failure type (`C0035-64`) saying how the part failed,
and the `!` marks one whose module is asking for a warning lamp. `P1234` reads
as Ford's definition because the VIN identified the car.

## Usage

```
go install github.com/fallais/cargo@latest

# or from a clone
go build -o cargo .

./cargo                 # terminal UI
./cargo --no-tui        # one-shot report
./cargo --vin           # print the VIN and exit
./cargo --detail        # report with each fault's history
./cargo modules         # find every module and say what it is
./cargo clear           # erase trouble codes, after asking
```

Keys: `d` dashboard, `c` codes, `v` vehicle, `a` adapter, `q` quit.

## Connecting

The UI starts without attaching to anything. The adapter page (`a`) lists the
devices it can see, and `enter` connects to the one you pick, because attaching
to a car is not something to do behind your back.

`t` on that page turns on autoconnect, which attaches to the first adapter that
answers and reattaches if the cable is pulled. `--autoconnect` starts that way.
The one-shot report has no picker, so it always connects on its own.

## Flags

| | |
|---|---|
| `--port` | adapter device (default: scan `ttyUSB*`, `ttyACM*`, `rfcomm*`) |
| `--baud` | serial rate (default: probe 38400, 9600, 115200, 230400, 500000) |
| `--make` | vehicle marque, overriding the selected vehicle for one run |
| `--autoconnect` | attach to the first adapter that answers, without being asked |
| `--theme` | `terminal` keeps your background, `dark` paints its own |
| `--no-tui` | print one report and exit |
| `--vin` | print the VIN alone and exit |
| `--detail` | show how often each fault happened, and at what mileage |
| `--sweep` | look for modules at every diagnostic address first (engine off) |
| `--debug` | verbose logging |

Any flag can also be set via `CARGO_*` environment variables or
`$XDG_CONFIG_HOME/cargo/cargo.yaml`.

## How it works

A car is a network of computers, the ECUs, one per system. Each keeps its own
fault memory and speaks its own protocol. A diagnostic tool reaches them
through an adapter plugged into the OBD-II port, and asks each one in turn.

<p align="center">
  <img src="assets/how-it-works.svg" alt="A diagnostic tool reaches the car's ECUs through an ELM327 adapter in the OBD-II port; each ECU sits on the CAN bus, speaks its own protocol and keeps its own fault memory" width="880">
</p>

Each code says which system, how the component failed, and what state the
fault is in:

<p align="center">
  <img src="assets/dtc-anatomy.svg" alt="Anatomy of the trouble code C1A60-7B with status A9: system, who defines it, component, failure type and status" width="880">
</p>

## Licence

MIT. See [LICENSE](LICENSE). The bundled trouble-code data comes from
third-party datasets; see [NOTICE](NOTICE).
