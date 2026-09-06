# Trouble-code catalog: format and provenance

## Why CSV

The catalog is a committed data file, so the format was chosen on how it
behaves in git, not on how small it is on disk. Measured on a synthetic
28,220-row catalog (the size of the largest open dataset), applying one
realistic maintenance update — 200 codes added, 100 descriptions corrected —
and running `git gc --aggressive` after each commit:

| format          | on disk | repo after v1 | after v2 | cost of the update | diff |
|-----------------|--------:|--------------:|---------:|-------------------:|------|
| **CSV**         | 1.10 MB |        172 KB |   176 KB |           **4 KB** | line per code |
| JSON (map)      | 1.21 MB |        179 KB |   184 KB |               5 KB | one line, unreadable |
| JSON (array)    | 2.37 MB |        211 KB |   216 KB |               5 KB | readable, 2x size |
| Parquet (zstd)  | 0.23 MB |        223 KB |   447 KB |             224 KB | binary |
| SQLite          | 1.67 MB |        480 KB |   923 KB |             443 KB | binary |

Parquet wins on disk and loses everything else. Both binary formats rewrite
their entire byte layout when rows change, so git stores a fresh copy on every
commit: the repository roughly doubles per update, and twenty updates turn a
1 MB dataset into ~9 MB of history. Neither produces a reviewable diff, which
matters more than size here — a data file whose provenance is uncertain needs
changes a human can actually read before merging.

CSV costs 4 KB per update, and a correction shows up as one changed line.

## GitHub compatibility

All of these are fine to commit; none approach a limit. GitHub warns above
50 MB per file and hard-rejects at 100 MB, and Git LFS is not needed until a
file is both large and frequently rewritten. At ~1.1 MB with 4 KB updates, the
CSV never gets close on either axis.

The binary formats are what would eventually push you toward LFS — not because
any single file is big, but because the history grows by a full copy each time.
That is a self-inflicted problem, and picking CSV avoids it.

## File layout

`internal/dtc/data/*.csv`, embedded with `go:embed`, so the binary is
self-contained and the tool works in a garage with no connectivity.

Each file declares its own columns, so the loader never has to guess:

```
# format: code,description          generic.csv
# format: code,make,description     manufacturer.csv, local.csv
```

Lines beginning with `#` and blank lines are ignored. **Records must be sorted**
by code, then make: the catalog binary-searches the embedded bytes rather than
building a map, and `NewIndexed` rejects an unsorted file rather than returning
quietly wrong answers.

### Why manufacturer codes carry a make

The same number means different things on different marques. Measured across
the imported datasets, **695 codes are defined incompatibly by two or more
makes**:

| code | make | description |
|---|---|---|
| P1106 | acura | BARO Circuit Range Performance Malfunction |
| P1106 | chevy | MAP Sensor Circuit Intermittent High Voltage |

A flat `code,description` table has to pick one, and picking wrong sends
someone to replace the wrong part. So manufacturer rows carry their make, and
with no `--make` set the resolver reports the ambiguity instead of choosing.

Sorting is what makes the CSV choice free at runtime. Against a full 28k-row
catalog:

| approach                    | startup | allocated | per lookup |
|-----------------------------|--------:|----------:|-----------:|
| parse into a map (`LoadCSV`) |   21 ms |    7.7 MB |         —  |
| index offsets (`NewIndexed`) | **3.1 ms** | **1.8 MB** | **489 ns** |

and it is behind a `sync.Once`, so a run that never resolves a code pays
nothing.

## Layers

Lookups fall through in order, first hit wins:

1. **User** — `$XDG_CONFIG_HOME/cargo/dtc.csv`, for codes specific to the
   owner's vehicle.
2. **`data/local.csv`** — hand-curated additions and corrections. **Not
   generated**: `cargo import` never touches this file, so fixes survive a
   re-import. Codes the upstream datasets miss belong here.
3. **`data/generic.csv`** — codes in the ISO/SAE-controlled ranges. Same
   meaning on every vehicle.
4. **`data/manufacturer.csv`** — vendor codes, tagged by make.
5. **Derived from the encoding** — not a file. A code carries its own system
   and whether it is generic or vendor-defined, so an unmatched code still
   reports "Powertrain, manufacturer-specific" rather than "unknown".

Every `Definition` carries its `Source`, and the UI shows it, so a curated
entry is visually distinguishable from a community guess.

## Importing

```sh
./cargo import              # fetch, classify, write, regenerate CREDITS.md
./cargo import --offline    # rebuild from the download cache
```

The importer classifies each code by **what its encoding says**, not by which
file it arrived in — the upstream `p_codes.txt` pools ISO/SAE and vendor codes
together, so `dtc.Kind` decides where each one lands.

Output is committed, so review the diff before merging and rebuild afterwards
for the change to reach the binary. The current import yields 9,075 generic and
8,883 manufacturer definitions across 32 makes, from these sources:

| source | licence | contents | imported |
|---|---|---|---|
| [Wal33D/dtc-database](https://github.com/Wal33D/dtc-database/) | MIT | 37 files: 5 pooled by code letter, 32 by marque | yes |
| [todrobbins/dtcdb](https://github.com/todrobbins/dtcdb) | MIT | small generic CSV | yes |
| [OBDb](https://github.com/OBDb) | **CC BY-SA 4.0** | per-make/model JSON, maintained daily | no — see below |
| [SAE J2012 (2002)](https://archive.org/stream/gov.law.sae.j2012.2002/sae.j2012.2002_djvu.txt) | US gov. law document | generic codes, cleanest provenance | no |

OBDb is deliberately excluded. Its share-alike terms would propagate to the
generated catalog and, through the embed, to anything distributing the binary —
a bigger licensing commitment than an MIT project should make silently. Add it
only as a knowing decision; the importer's `Licence` field is where that would
be recorded.

To add a source, append to `Sources()` in `internal/dtcimport/source.go` with
its parser and licence. Tests enforce that every source declares one.

### A caveat worth keeping

An MIT licence on a scraped dataset does not settle the rights to the
underlying definitions. The authoritative source, SAE J2012, is paywalled, and
its machine-readable Digital Annex is a paid product; every free list is a
derivative of it. Manufacturer codes are genuinely proprietary — J2012 reserves
the ranges but not the meanings.

That is the reason for the derived fallback: for a vendor code the honest
answer is "manufacturer-specific, consult service documentation", not a guess
borrowed from a list compiled for a different make.
