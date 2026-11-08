# APS18 production records

`APS18` is the record APsystems' `main.exe` writes every 5 minutes into
`/home/record.db` (table `Data`, see [SERVICE_MODE.md](SERVICE_MODE.md)) for
`client` to upload to EMA. It is the only place where some inverter values
exist on the ECU — notably the measured DC current per channel and the
inverter's own running-time and energy-today counters; neither the web UI,
the local API (port 8899) nor APsystems' Modbus server (port 502) expose
them.

This document describes the format as decoded on an ECU-C-Z (fw C1.2.27A)
with a single QT2, and the parser / generator in
[`internal/source/aps18.go`](../internal/source/aps18.go) and
[`internal/source/aps18_gen.go`](../internal/source/aps18_gen.go).

## How it was decoded

- 144 consecutive records (2026-11-07 08:20 → 2026-11-08 11:55), kept as
  the test fixture [`testdata/aps18/qt2.txt`](../testdata/aps18/qt2.txt).
- Cross-checked against `/tmp/parameters_app.conf` and
  `/tmp/parameters.conf` captured at the same cycles, and against an EMA
  "DC Performance" export for the same day, which labels the per-channel
  values `DC-P` (W), `DC-V` (V) and `DC-I` (A).
- No field is a checksum: none of the usual families (digit sums, byte
  sums mod 256/1000, XOR, CRC32) matches any field, and every decoded
  relation below holds on all 144 records.

## Format

One fixed-width ASCII line, numbers zero-padded decimals, stored with a
trailing newline (`…END\n`).

Header, terminated by `END`:

| Width | Example | Meaning |
|------:|---------|---------|
| 5 | `APS18` | record type |
| 2 | `AA` | constant |
| 3 | `293` | record length without the trailing newline (294 characters stored for one QT2) |
| 8 | `AAAAAAA1` | constant |
| 12 | `215000054677` | ECU ID |
| 10 | `0000030900` | system power, W × 100 |
| 10 | `0000024744` | energy of the 5-minute interval, Wh × 1000 |
| 10 | `0000010864` | ECU lifetime energy, kWh × 10 |
| 14 | `20261108112000` | interval end, `YYYYMMDDhhmmss`, on the 5-minute mark |
| 9 | `001100000` | constant on this site |

Then per inverter (one QT2 here), terminated by `END`:

| Width | Example | Meaning |
|------:|---------|---------|
| 12 | `901500081373` | inverter UID |
| 2 | `16` | constant (type/variant code) |
| 5 | `00499` | grid frequency, Hz × 10 |
| 3 | `118` | temperature, °C + 100 |
| 47 × 3 | | channels 1–3, one per phase |
| 41 | | channel 4 |

Channels 1–3:

| Width | Example | Meaning |
|------:|---------|---------|
| 1 | `1` | channel number |
| 5 | `00375` | DC voltage, V × 10 (EMA `DC-V`) |
| 3 | `023` | DC current, A × 10, sampled at the cycle (EMA `DC-I`) |
| 7 | `0008248` | phase energy of the interval, Wh × 1000 |
| 7 | `0005838` | channel energy of the interval, Wh × 1000 |
| 4 | `0073` | channel power, W (EMA `DC-P`) |
| 5 | `00399` | line voltage of the phase (AB, BC, CA), V |
| 15 | `000…` | zeros |

Channel 4 (no phase of its own) carries the inverter-level counters:

| Width | Example | Meaning |
|------:|---------|---------|
| 1 | `4` | channel number |
| 5 | `00375` | DC voltage, V × 10 |
| 3 | `024` | DC current, A × 10 |
| 7 | `0006459` | channel energy of the interval, Wh × 1000 |
| 4 | `0081` | channel power, W |
| 6 | `000760` | unknown; 758–762 while producing, erratic at dawn and dusk |
| 5 | `12975` | inverter running time today, s |
| 5 | `05603` | inverter energy today, Wh × 10 |
| 5 | `00000` | zeros |

## Relations

Checked on all 144 records:

- system power = 100 × Σ channel power (exact);
- interval energy = Σ channel energy + 0…3 mWh: channel energies are
  truncated, the total is computed before truncation;
- phase energy = ⌊interval energy ÷ 3⌋ (143/144; the last one is the same
  rounding effect) — the QT2's output is split evenly over the phases;
- DC current is the value at the cycle, not W ÷ V: on 2026-11-07 at 13:20
  it read 8.8–9.4 A while the 5-minute average power gave 4.7–4.9 A, and
  it reads 0 whenever production is near zero, even with the inverter
  reporting;
- running time advances by the elapsed time (300 s ± ~10 s per record)
  and energy today by the interval energy (the inverter's own counter
  drifts a few % from the channel sum); both reset when the inverter
  restarts at dawn or dusk, and advance less when it runs only part of an
  interval at dawn.

EMA displays these values directly (DC power, voltage and current per
channel) and builds its daily energy from the interval energies it
receives; it does not reconcile with the cumulative counters afterwards
(2026-11-07: EMA 2.2 kWh, inverter 2.93 kWh after two Service Mode
windows).

## Parser

```go
r, err := source.ParseAPS18(line) // line includes the trailing "\n"
s := r.Format()                   // byte-identical to line
```

`ParseAPS18` decodes the single-QT2 layout above into `APS18` /
`APS18Inverter` / `APS18Channel`; any other layout (other inverter types,
mismatching length field, non-zero padding) is rejected rather than
guessed. `Format` re-encodes a record; the test suite round-trips all 144
fixture records byte for byte.

## Generator

```go
next, err := source.NextAPS18(prev, input)
```

`NextAPS18` computes the record for the interval ending at `input.Time`
from what the adapter measures (`APS18Input`: per channel the last power,
the energy integrated over the interval, DC voltage and current; per
inverter frequency, temperature and line voltages; the ECU lifetime
energy), using `prev` — the record of the previous interval — for what it
cannot measure: the constant codes, the unknown channel-4 field, and the
inverter's running-time and energy-today counters, which it advances by
the interval.

Validation: for each of the 120+ pairs of consecutive fixture records,
generating the second from the first plus the second's measurements
reproduces every derived field exactly; the inverter-kept counters stay
within the measured jitter (running time ±10 s, energy today ±6 Wh),
except at dawn/dusk restarts, which the test counts (3 in the fixture).

Limits:

- one QT2 layout only; other inverter types or several inverters per
  record have not been observed;
- the channel-4 `000760` field is carried over from `prev`;
- inverter counter resets at dawn/dusk can't be predicted.

The generator only builds records; nothing in `ecu-sunspec` writes them
anywhere.
