# cargo

A command-line OBD-II tool. Reads live data and diagnostic trouble codes from a
vehicle through an ELM327 adapter.

```
go build -o cargo .
./cargo                 # terminal UI
./cargo --mock          # try it without a car
./cargo --no-tui        # one-shot report
```

The UI starts whether or not an adapter is attached, and connects on its own
once one appears.

## Flags

| | |
|---|---|
| `--port` | adapter device (default: scan `ttyUSB*`, `ttyACM*`, `rfcomm*`) |
| `--baud` | serial rate (default: probe 38400, 9600, 115200, 230400, 500000) |
| `--make` | vehicle marque, to disambiguate manufacturer codes |
| `--mock` | simulated vehicle |
| `--no-tui` | print one report and exit |
| `--debug` | verbose logging |

Any flag can also be set via `CARGO_*` environment variables or
`$XDG_CONFIG_HOME/cargo/cargo.yaml`.

## Trouble codes

Codes are read from every ECU that answers — stored (mode 03), pending (07) and
permanent (0A) — and reported with the module they came from and how confirmed
they are. Non-emissions modules such as ABS and airbag are addressed on a
best-effort basis: most answer UDS `0x19` rather than OBD-II mode 03, which is
not yet implemented.

Descriptions come from an embedded catalog of ~18,000 definitions. A code with
no entry is still described from its own encoding rather than reported as
unknown, and manufacturer codes that several marques define differently ask for
`--make` instead of guessing.

Rebuild the catalog from the upstream datasets with:

```
./cargo import
```

See [docs/dtc-data.md](docs/dtc-data.md) for the data format and why it is CSV.

## Licence

MIT — see [LICENSE](LICENSE). The bundled trouble-code data comes from
third-party datasets; see [CREDITS.md](CREDITS.md).
