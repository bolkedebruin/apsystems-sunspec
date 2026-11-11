# ECU Service Mode and the EMA upload queue

Findings from an ECU-C-Z (fw C1.2.27A) running one QT2, measured on
2026-11-07. Other firmware revisions may behave differently.

## Service Mode (fast polling)

The ECU web UI (Real Time Data page) offers a "Service Mode" button. Its
help text (`fast_polling_text` in
`/home/local_web/pages/application/language/english/page_lang.php`) says:

> When working in service mode, the data refreshing will be quick,
> convinient for service at site, and the data will NOT be sent to EMA at
> this mode, after 30 minutes, service mode will quit automatically.

What actually happens:

- **The flag is a file.** `/etc/yuneng/fast_polling.conf` containing `1`
  turns Service Mode on; `0` (or no file) is normal mode. The web view
  reads it with `intval(fgets())`; `main.exe` reads the same path and logs
  `fast_polling...`. It picks up a change within one cycle, no restart.
- **The web toggle is broken.** The button calls
  `management/set_polling_conf`, which calls
  `management_model->set_polling_file()`. That function does not exist
  anywhere under `/home/local_web`, so the button always answers
  "Change failed" (`set_polling_file_1`).
- **Polling cadence.** Normal mode: one radio cycle every 5 minutes.
  Service Mode: the inverter row of `/tmp/parameters_app.conf` changes
  every ~21 s. The header timestamp of that file only advances once a
  minute and is truncated to the minute (`…122700`, `…122800`), so the
  file mtime is the reliable "last poll" time.
- **Automatic exit after 60 minutes, not 30.** Set at 12:27:26, the flag
  was still `1` at 13:00:51 (33 min). Set over Modbus at 13:47:20, the
  file was rewritten to `0` (one byte, no newline — not written by
  `ecu-sunspec`, which writes `0\n`) at 14:47:36, 60 min later, with no
  other writer involved.

`ecu-sunspec` exposes this as vendor Model 64203 (see
[`sunspec-models/model_64203.json`](../sunspec-models/model_64203.json)):
`FastPoll` and `FastPollThresholdW` (read/write), `FastPollElapsedS`,
`LastPollTms`, `DataAgeS`.

| `FastPoll` | Behaviour |
|-----------:|-----------|
| 0 | normal 5-minute polling; the adapter leaves the flag alone |
| 1 | Service Mode, re-enabled by the adapter whenever main.exe drops it |
| 2 | automatic: Service Mode while system production ≥ `FastPollThresholdW` (default 50 W), normal mode once it has stayed below threshold − max(10 W, 20 %) for 2 minutes, at most one switch every 10 minutes |

The choice is saved in `/home/sunspec-fastpoll.conf` (`<mode> <threshold>`,
written only when it changes) and restored after a restart. In automatic
mode EMA only receives the low-production parts of the day (dawn, dusk,
heavy overcast): it keeps its own daily total from the records it gets
and does not catch up afterwards — on 2026-11-07 EMA kept 2.2 kWh while
the inverter counted 2.93 kWh.

Home Assistant example (native Modbus integration: mode selector,
threshold, data-age and EMA sensors, stalled-upload alert):
[`examples/home-assistant/ecu-control.yaml`](../examples/home-assistant/ecu-control.yaml).

## What reaches EMA: `/home/record.db`

`/home/applications/client` uploads to EMA ("Connect to EMA data
successfully", "Failed to send data") from `/home/record.db`:

```sql
CREATE TABLE Data(item INTEGER, record VARCHAR(74400), resendflag VARCHAR(20), date_time VARCHAR(16))
```

Two kinds of rows are queued, each every 5 minutes in normal mode:

| `record` starts with | Size  | Timing                     | Content |
|----------------------|-------|----------------------------|---------|
| `APS18…`             | ~294  | on the 5-minute mark       | inverter production: ECU ID, then each inverter UID with its values |
| `{"VER":"M0001",…`   | ~675  | ~5 min, not aligned        | JSON meter readings: `"E":"c"` (consumption) and `"E":"p"` (production) blocks with frequency (`O`) and voltage (`P`); power (`F`) is 0 without current sensors |

### Queue life cycle

From the SQL strings in `main.exe` and `client`:

- `main.exe` appends each row with
  `INSERT INTO Data (item, record, resendflag, date_time) VALUES(%d, '%s', '1', '%s')`;
  `item` is shared by both kinds of rows and increments by one.
- `client` picks rows to send with
  `SELECT record,date_time FROM Data WHERE resendflag='1' … ORDER BY date_time DESC LIMIT %d`
  (separately for `record LIKE 'APS%'` and the rest), and also queries
  `resendflag='2'`. It can drop the oldest row
  (`DELETE FROM Data WHERE rowid=(SELECT MIN(rowid) FROM Data)`), but the
  table mostly grows: 504 rows / 360 KB on 2026-11-08. `main.exe` rotates
  the whole file when it gets large (`mv /home/record.db /home/recordA.db`,
  up to `recordN.db`; this ECU has a 30 MB `recordA.db` from 2026-11-07
  08:18).
- Observed: the newest row of each kind sits at `2` until the next row
  of that kind arrives, older rows are `0`.

`client` keeps a persistent TLS connection to EMA (port 9005), and
`control_client` contacts the EMA control server (port 9003) every
15 minutes, logging each session in `/home/control_client.log`. Both
binaries also reference a `fill_up_data(data, lost_date_time, send_flag)`
table for lost inverter data; it does not exist on this ECU.

### `APS18` production record

The inverter production rows are fixed-width `APS18…` records; their
format, how it was decoded, and the parser / generator in
`internal/source` are described in [APS18.md](APS18.md). EMA builds its
power curve and daily energy from these records: the daily total is the
sum of the interval energies it received.

### Effect of Service Mode on EMA

Observed over two windows (12:27–13:01 and 13:47–14:47):

- The JSON meter rows kept coming every ~5 minutes.
- **No `APS18` row was written** while Service Mode was on (none between
  13:45:00 and 14:55:00 for the second window). Production rows resume
  with the first normal cycle afterwards; even a 2-minute Service Mode
  cost one 5-minute row.
- The missing rows are never backfilled. EMA repeats the last received
  value for ~25 minutes (13:50–14:10 equal to 13:45), then shows a gap
  until data resumes (14:55).
- **EMA loses the energy produced meanwhile.** The ECU's own counters
  (`historical_data.db`, Modbus `WH` / Model 64202 `TodayWh`) keep
  counting, but EMA integrates the power samples it receives: at 16:00 the
  ECU reported 2.748 kWh for the day, EMA 2.11 kWh.

Service Mode is therefore a short diagnostic tool, not something to leave
on or toggle on a schedule.

APsystems makes some warranties depend on the system staying connected to
EMA through the ECU (for example the 2025 Asia-Pacific microinverter
terms reduce DS/QT coverage to 2 years without a continuous connection).
Check the warranty text for your region before using Service Mode often.

## Monitoring EMA uploads over Modbus

Model 64203 also publishes, refreshed at most once a minute:

| Register | Field | Source |
|---------:|-------|--------|
| 40618 | `EmaConnected` | `/tmp/connectemaflag.txt` (`connected`) |
| 40619 | `LastProdRecTms` | newest `APS18` row in `/home/record.db` |
| 40621 | `ProdRecPending` | `APS18` rows with `resendflag` ≠ `0` |
| 40622 | `ProdRecLastHour` | `APS18` rows in the last hour (12 normally, 0 in Service Mode) |
| 40623 | `LastEmaCtrlTms` | last "Connect to EMA successfully" in `/home/control_client.log` (every 15 min) |
| 40625 | `ProdRecAgeS` | seconds since `LastProdRecTms` |

`record.db` and the log sit on the ECU's flash and belong to APsystems'
processes: `ecu-sunspec` copies `record.db` into `/tmp` (tmpfs, RAM),
reads the copy read-only and deletes it, and only reads the last 8 KB of
the log, so nothing is written to flash and the live database is never
locked.
