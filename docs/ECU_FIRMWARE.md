# ECU-C firmware: processes, ports and databases

Inventory of the APsystems-specific software on an ECU-C-Z, firmware
C1.2.27A (Linux, ARMv7, BusyBox), taken on 2026-11-08 with one QT2
attached. Generic Linux daemons (udev, syslog, dbus, getty, udhcpc…) are
left out.

Roles come from each binary's strings (file paths, SQL, log lines,
hostnames) and from observed behaviour; where a role is only inferred
from names it says so.

## Process tree

Almost every APsystems program runs under a small watchdog that restarts
it when it exits (`killall main.exe` → back within ~14 s). All live in
`/home/applications/`.

| Program | Watchdog | Role |
|---------|----------|------|
| `main.exe` | `monitor.exe` | Core: ZigBee polling of the inverters, `/tmp/parameters_app.conf` / `/tmp/parameters.conf`, energy history, EMA records (`record.db`), grid-protection parameters, Service Mode (`/etc/yuneng/fast_polling.conf`), APsystems' Modbus/SunSpec server on TCP 502. Restarted every night around 02:12 (observed). |
| `client` | `clientmonitor` | Uploads `record.db` to EMA over a persistent TLS connection (`ecu.apsystemsema.com`, port 9005). Log `/home/client.log`. |
| `control_client` | `control_client_monitor` | EMA control channel: every 15 min (`Report_Interval` in `controlclient.conf`) asks `ecu.apsystemsema.com:9003` for commands; can write many `/etc/yuneng` settings, including `fast_polling.conf`. Log `/home/control_client.log`. |
| `mqtt.exe` | `mqtt_monitor.exe` | MQTT link to `data.mqtt.apsystemsema.com` (power limits, on/off, results in `mqtt.db`). |
| `server_app` | `phone_server_app_monitor` | Local API on TCP 8899 (`APS11…END` commands used by the mobile app and Home Assistant's ECU reader; `set_serviceMode`). Log `/home/server_app.log`. |
| `lancommClient` | `lancommClientMonitor` | UDP 8899; LAN link to APsystems storage and meters (`apstorge*`, `meterConfig*` settings). Log `/home/lanCommClient.log`. |
| `phone_server` | `phone_server_monitor` | TCP 4560; app-facing server reading `system_p_display.conf` and the energy history (inferred: legacy app protocol). |
| `idwriter` | — | TCP 4540; inverter ID / commissioning tool (writes inverter IDs, Wi-Fi AP info; inferred from name and files). |
| `meterManager` | `meterManagerMonitor` | Energy meter (CT) handling over `/tmp/meter_ipc_socket`; feeds the `meter_*` tables. |
| `single_update` | `single_update_monitor` | Single-inverter firmware update (`/tmp/update_single.tar.bz2`). |
| `autoupdate`, `autoupdate2`, `autoupdate_restart` | `updatemanager` | ECU firmware updates from `ecueu.apsystemsema.com` / `ecush.apsystemsema.com`; run the package's `assist` script. Log `/home/autoupdate.log`. |
| `AutoUpInver2` | — | Automatic inverter firmware updates. Log `/home/AutoUpInver.log`. |
| `network.exe` | — | Ethernet / Wi-Fi / DHCP configuration. |
| `gprs` | `gprsmonitor` | Cellular data path; `quectel_monitor.exe` watches the 4G module. |
| `apbutton`, `buttonreset` | — | Front button: Wi-Fi access point (`wlan1`, udhcpd) and reset. |
| `led.exe` | `ledMonitor.exe` | Status LEDs, driven by `/tmp/connectemaflag.txt` and `/tmp/led.conf`. |
| `resmonitor` | — | Resource watchdog (`stopflag.conf`; inferred). |
| `usbTest` | — | USB checks. Log `/home/usbTest.log`. |
| `ecu-sunspec` | init (`S99-sunspec`) | This adapter: Modbus/SunSpec on TCP 1502. |

Present but not running at inventory time: `AutoUpInver`, `apstore2`,
`button`, `diagnosis_network2`, `drm_connect.exe`, `eculocation`,
`ipcTest`, `macapp`, `manager`, `mdnsDiscovery(+Monitor)`, `ntpapp.exe2` /
`ntpdate` / `ntpmanager`, `quectel-CM`, `rapidly_shutdown`, `rtc_app`,
`setUpTimes`, `slave485` (RS485), `udpDiscovery(+Monitor)`,
`updateHeart`, `wifi_init`, `wifindaa`, `xiaocontrol_client`, plus
bundled `curl` and `wget`.

## Listening ports

| Port | Program | Service |
|-----:|---------|---------|
| TCP 22 | dropbear | SSH (installed by the `-dropbear` package of this project) |
| TCP 80, 443 | lighttpd + php-cgi | ECU web UI (`/home/local_web`, CodeIgniter) |
| TCP 502 | `main.exe` | APsystems Modbus/SunSpec: unit 247 = ECU (models 1, 701, 702, 704, 715), inverters at the unit IDs in `database.db` `modbus_addr` (here 42: models 1, 103, 113, 123) |
| TCP 1502 | `ecu-sunspec` | this adapter |
| TCP 4540 | `idwriter` | commissioning tool |
| TCP 4560 | `phone_server` | app server |
| TCP 8080 | thttpd | static files from `/srv/www` |
| TCP 8899 | `server_app` | local API |
| UDP 8899 | `lancommClient` | LAN storage/meter link |

Outbound: `client` → EMA data (9005), `control_client` → EMA control
(9003), `mqtt.exe` → EMA MQTT, `autoupdate` → update centre.

## Databases

All SQLite, in `/home` (on flash).

### `database.db` — configuration and inverter state

| Table | Rows | Content |
|-------|-----:|---------|
| `id` | 1 | inverters: UID, ZigBee short address, `model` (41 = QT2), software version, phase, bind flags |
| `modbus_addr` | 1 | inverter UID → Modbus unit ID on port 502 |
| `signal_strength` | 4 | ZigBee RSSI per inverter |
| `power` | 3 | per-inverter power limits (`limitedpower`, `stationarypower`) |
| `powerall`, `ltpower`, `tdpower` | 0–1 | system power limits, lifetime / today power |
| `protection_parameters60code` | 1 | active grid-protection parameters per inverter, as two-letter codes (`AA`…`TL`) |
| `protection_parameters60` | 4 | the same with readable column names |
| `protection_parameters60_info` | 1547 | parameter catalogue per grid specification: names (EN/CN), defaults, ranges, codes |
| `set_protection_parameters`, `set_protection_parameters_inverter`, `set_parameters60_artificial`, `set_inverter_new` | 0–2 | queues of parameter writes for `main.exe` to send |
| `turn_on_off`, `clear_gfdi`, `need_boot`, `ird`, `update_inverter` | 0–3 | other command queues (on/off, GFDI reset, reboot, IRD, inverter updates) |
| `process_result`, `inverter_process_result` | 12–17 | results of those commands |
| `Event` | 184 | alarm bitstrings per inverter and date (decoded in [EVENTS.md](EVENTS.md)) |
| `EVENT2` | 1 | latest event summary |
| `protection_parameters` | 0 | legacy protection table |

Rows for inverters no longer attached (here two `704…` UIDs) stay in
`power` and `protection_parameters60`.

### `historical_data.db` — energy and power history

| Table | Rows | Content |
|-------|-----:|---------|
| `each_system_power` | 17136 | system power every cycle (`date`, `time`) |
| `daily_energy`, `monthly_energy`, `yearly_energy`, `lifetime_energy` | 136 / 7 / 3 / 1 | ECU energy counters (kWh) |
| `meter_power` | 72012 | meter power per phase (`powerA/B/C`) per `chip` |
| `meter_daily_energy`, `meter_monthly_energy`, `meter_yearly_energy`, `meter_lifetime_energy` | 254 / 10 / 4 / 2 | meter energy counters per `chip` |

The adapter reads the energy counters from here (Model 101/103 `WH`,
Model 64202).

### `record.db` — EMA upload queue

`Data(item, record, resendflag, date_time)` holds the `APS18` production
records and JSON meter records `client` uploads; see
[SERVICE_MODE.md](SERVICE_MODE.md) and [APS18.md](APS18.md).
`inverter_status` and `inverter_status_single` are empty here.
`main.exe` rotates the file when it grows (`recordA.db` … `recordN.db`;
`recordA.db`, 30 MB, has the same schema).

### Other databases

| File | Content |
|------|---------|
| `inverterRecord.db` | `last_connect_time(id, linux_time, connect_type)`: last radio contact |
| `mqtt.db` | MQTT command queues (`setMaxPower`, `setOnOff`, per inverter) and `post_result` / `post_cause` |
| `mqtt_record.db` | MQTT upload queues `miData`, `miAlarm` (empty here); rotated like `record.db` |
| `apsdatabase.db`, `encryption.db`, `xiaodatabase.db` | empty (0 bytes) on this ECU |
| `/tmp/replacement.db` | empty; the web UI's `replacement` view lists a `lost_all_time` table |

## Exchange files

`main.exe` publishes live data as small files in `/tmp` (tmpfs, RAM), which
the other programs and this adapter read:

| File | Content |
|------|---------|
| `parameters_app.conf` | per inverter: online flag, type, frequency, temperature, channel powers, line voltages; header stamped with the cycle time (minute resolution) |
| `parameters.conf` | per channel: power, DC voltage, frequency, line voltage, temperature (the web UI's Real Time Data table) |
| `system_p_display.conf`, `real_time_data.txt` | system power, lifetime energy |
| `powerALL.conf` | fleet capacity computed by `main.exe` |
| `connectemaflag.txt` | `connected` while `client` is connected to EMA |
| `onlineNumber.conf`, `total_inverter.txt` | inverter counts |

Persistent settings are one-value or `key=value` files in `/etc/yuneng`
(`ecuid.conf`, `version.conf`, `model.conf`, `timezone.conf`,
`controlclient.conf`, `fast_polling.conf`, …).
