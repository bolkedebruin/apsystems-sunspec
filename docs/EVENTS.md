# APsystems event-bit decode

This document maps the 86-bit event bitstring stored in the ECU's
`/home/database.db Event` table (column `eve`) to its semantics, taken from
the firmware's own UI language file:
`/home/local_web/pages/application/language/english/page_lang.php`
(`display_status_zigbee_<N>` keys).

The same bitstring is what `ecu-sunspec` exposes:

- **Standard SunSpec `Evt1`** — alarm-class flags translated by
  `MapAPsystemsToSunSpecEvt1` so generic SunSpec consumers (HA, Venus, etc.)
  see named alarms (Ground Fault, AC Over Volt, Over Temp, …) without
  needing this table.
- **`EvtVnd1` / `EvtVnd2` / `EvtVnd3`** — the raw bits 0-31, 32-63, 64-95.
  Use these when you want full fidelity (per-channel voltage stages,
  per-panel arc fault, signal timeout, etc.).

The bit positions are stable across firmware versions (verified through
2.1.29D) but new bits may be added at the high end with each firmware
release.

## Full bit table (positions 0-83)

| Bit | SunSpec mapping            | APsystems name (English)                   |
|----:|----------------------------|---------------------------------------------|
|   0 | Evt1OverFrequency          | AC Frequency Exceeding Range                |
|   1 | Evt1UnderFrequency         | AC Frequency Under Range                    |
|   2 | Evt1ACOverVolt             | Channel A: AC Voltage Exceeding Range       |
|   3 | Evt1ACUnderVolt            | Channel A: AC Voltage Under Range           |
|   4 | Evt1ACOverVolt             | Channel B: AC Voltage Exceeding Range       |
|   5 | Evt1ACUnderVolt            | Channel B: AC Voltage Under Range           |
|   6 | Evt1ACOverVolt             | Channel C: AC Voltage Exceeding Range       |
|   7 | Evt1ACUnderVolt            | Channel C: AC Voltage Under Range           |
|   8 | Evt1DCOverVolt             | Channel A: DC Voltage Too High              |
|   9 | (none — vendor only)       | Channel A: DC Voltage Too Low               |
|  10 | Evt1DCOverVolt             | Channel B: DC Voltage Too High              |
|  11 | (none — vendor only)       | Channel B: DC Voltage Too Low               |
|  16 | Evt1OverTemp               | Over Critical Temperature                   |
|  17 | Evt1GroundFault            | GFDI Locked                                 |
|  18 | Evt1ManualShutdown         | Remote Shut                                 |
|  19 | Evt1ACDisconnect           | AC Disconnect                               |
|  21 | Evt1GridDisconnect         | Active Anti-island Protection (freq-shift!) |
|  22 | (none — vendor only)       | CP Protection                               |
|  23 | Evt1ACOverVolt             | AC Voltage Exceeding Range (legacy)         |
|  24 | Evt1ACUnderVolt            | AC Voltage Under Range (legacy)             |
|  25 | (none — vendor only)       | 10min Protect                               |
|  26 | (none — vendor only)       | BUS Voltage Too Low                         |
|  27 | (none — vendor only)       | BUS Voltage Too High                        |
|  28 | Evt1HWTestFailure          | Relay Failed                                |
|  29 | Evt1OverFrequency          | AC Frequency stage-1 Exceeding Range        |
|  30 | Evt1UnderFrequency         | AC Frequency stage-1 Under Range            |
|  31 | Evt1OverFrequency          | AC Frequency stage-2 Exceeding Range        |
|  32 | Evt1UnderFrequency         | AC Frequency stage-2 Under Range            |
|  33 | Evt1ACOverVolt             | AC Voltage stage-2 Exceeding Range          |
|  34 | Evt1ACUnderVolt            | AC Voltage stage-2 Under Range              |
|  35 | Evt1ACOverVolt             | AC Voltage stage-3 Exceeding Range          |
|  36 | Evt1ACUnderVolt            | AC Voltage stage-3 Under Range              |
|  37 | Evt1ACOverVolt             | AC Voltage stage-4 Exceeding Range          |
|  38 | Evt1ACUnderVolt            | AC Voltage stage-4 Under Range              |
|  39 | Evt1DCOverVolt             | Channel C: DC Voltage Too High              |
|  40 | (none — vendor only)       | Channel C: DC Voltage Too Low               |
|  41 | Evt1DCOverVolt             | Channel D: DC Voltage Too High              |
|  42 | (none — vendor only)       | Channel D: DC Voltage Too Low               |
|  43 | (none — vendor only)       | Get Data Failed                             |
|  44 | Evt1ACOverVolt             | AC Voltage stage-1 Exceeding Range          |
|  45 | Evt1ACUnderVolt            | AC Voltage stage-1 Under Range              |
|  46 | (none — vendor only)       | AC-Parameter setting errors                 |
|  47 | (none — vendor only)       | Varistors Protection                        |
|  48 | (none — vendor only)       | AB-line stage-1 over-voltage (3-phase)      |
|  49 | (none — vendor only)       | AB-line stage-1 under-voltage               |
|  50 | (none — vendor only)       | AB-line stage-2 over-voltage                |
|  51 | (none — vendor only)       | AB-line stage-2 under-voltage               |
|  52 | (none — vendor only)       | AB-line stage-3 voltage                     |
|  53 | (none — vendor only)       | AB-line stage-3 voltage                     |
|  54 | (none — vendor only)       | AB-line stage-4 voltage                     |
|  55 | (none — vendor only)       | AB-line stage-4 voltage                     |
|  56-63 | (none — vendor only)    | BC-line stage-1..4 voltages                 |
|  64-71 | (none — vendor only)    | CA-line stage-1..4 voltages                 |
|  72 | (none — vendor only)       | CP1 protection                              |
|  73 | (none — vendor only)       | CP2 protection                              |
|  74 | (none — vendor only)       | Over-temperature derating                   |
|  75 | (none — vendor only)       | Over-frequency derating                     |
|  76 | (none — vendor only)       | Over-voltage derating                       |
|  77 | (none — vendor only)       | Channel A arc fault                         |
|  78 | (none — vendor only)       | Channel B arc fault                         |
|  79 | (none — vendor only)       | Channel C arc fault                         |
|  80 | (none — vendor only)       | Channel D arc fault                         |
|  81 | (none — vendor only)       | Channel A overcurrent                       |
|  82 | (none — vendor only)       | Channel B overcurrent                       |
|  83 | (none — vendor only)       | D-signal timeout                            |

(Bits 12-15, 20 are not assigned in this firmware.)

Bits 48-71 use Chinese strings in `page_lang.php`; English equivalents above
are paraphrases. The structure is `{AB,BC,CA}-line × stage-{1,2,3,4} ×
{over,under}-voltage`.

## Notes for the freq-shift use case

If you're driving an AC-coupled installation via Victron freq-shift, the
bits to watch are:

- **Bit 21** (`Active Anti-island Protection`) — fires when the inverter
  hits its anti-island trip; this is the bit that flips when your Multi
  raises Hz to throttle.
- **Bits 0, 29, 31** (over-frequency variants) — the Multi's freq-shift
  causes these as it ramps Hz up.
- **Bit 19** (AC Disconnect) — full disconnect.

In SunSpec these all surface in `Evt1`:
`OverFrequency | GridDisconnect | ACDisconnect`. Build HA automations on
those flags rather than per-bit.

## Latched vs transient

Empirically, several bits stay set indefinitely (e.g., bits 9, 11 — DC
voltage low — were set on every poll on this site even with the panels
producing). They appear to be *latched* — set when first observed, never
cleared until the inverter is power-cycled or restarted. Use for forensics,
not real-time alarms.

The standard-SunSpec `Evt1` mapping deliberately excludes these latched DC
flags so HA's alarm panel doesn't constantly show "DC fault" for healthy
inverters. They remain visible in `EvtVnd1`/`EvtVnd2` for owners who want to
see them.
