# apsystems-sunspec

A SunSpec / Modbus TCP adapter that turns an APsystems ECU (ECU-R, ECU-R-Pro, ECU-C, etc.) into a Fronius-compatible PV inverter for Victron GX, Home Assistant, Grafana, and any other SunSpec consumer.

The adapter reads from the ECU's existing SQLite databases and `/tmp/parameters_app.conf`, then publishes a standards-compliant SunSpec register bank on a configurable Modbus TCP port. It runs on the ECU itself or as a sidecar.

## What it exposes

| Modbus unit ID | Bank | Why |
|---|---|---|
| **1** | Aggregate: `Common + Inverter (101) + Nameplate (120) + Basic Settings (121) + Multi-MPPT (160 with all panels) + Vendor (64202) + End` | System-level totals; what Victron's Fronius driver reads |
| **2..N+1** | Per-microinverter: `Common (SN = inverter UID) + Inverter (101) + Multi-MPPT (160 with that inverter's panels) + End` | Per-inverter dashboards in HA / Grafana |

Each microinverter shows up in HA's SunSpec integration as an independent device. Per-panel data lives in Multi-MPPT (Model 160) — 2 modules per DS3, 4 per DS3-L.

Standard SunSpec event flags are populated from the ECU's own alarm bitstring: ground fault, over-temperature, AC over/under voltage, over/under frequency, manual shutdown, AC disconnect, grid disconnect (anti-island trip), HW test failure. Raw APsystems bits remain in `EvtVnd1..3` for full fidelity. See [`docs/EVENTS.md`](docs/EVENTS.md) for the complete bit table.

## Verify it's working

A quick sanity check against any SunSpec scanner. Using `pysunspec2`:

```sh
pip install pysunspec2 pyserial
python3 -c "
import sunspec2.modbus.client as c
d = c.SunSpecModbusClientDeviceTCP(slave_id=1, ipaddr='<ECU-IP>', ipport=1502, timeout=3)
d.scan()
for m in d.model_list: print(m.model_id, m.model_len)
"
```

Expected: `1, 101, 120, 121, 160, 64202` for unit 1.

## Building

```sh
make ecu      # ARMv7 binary for the ECU itself (~8 MB, statically linked)
make sidecar  # x86_64 binary for a sidecar host (Synology, generic Linux server)
make mac      # local development build
make test     # unit + integration tests
```

The build is pure Go (`CGO_ENABLED=0`) so the ARMv7 binary is glibc-version-independent and runs on the ECU's old userland.

## Installing on the ECU

The ECU exposes a local web endpoint that accepts a `tar.bz2` package and runs an embedded `assist` script after extraction. The Makefile builds the right shape of package directly.

### 1. Build the package

```sh
make package
# produces dist/apsystems-sunspec-<version>.tar.bz2
```

### 2. POST it to the ECU

```sh
curl -X POST -F file=@dist/apsystems-sunspec-<version>.tar.bz2 \
     http://<ECU-IP>/index.php/management/exec_upgrade_ecu_app
```

The PHP handler extracts the tarball into `/home/update_from_app/` and runs `update_localweb/assist`. The script:

- copies `ecu-sunspec` to `/home/applications/`
- installs `/etc/init.d/S99-sunspec` (auto-start on boot)
- restarts the adapter

A log of the install lands in `/home/sunspec-install.log` on the ECU.

### 3. Confirm

```sh
nc -zv <ECU-IP> 1502   # should connect
```

…and re-run the pysunspec2 verify above.

### Including dropbear (SSH on the ECU)

To bundle a dropbear SSH server in the same install package:

```sh
# 1. Fetch dropbear binaries that match the ECU's glibc generation.
make fetch-dropbear
# downloads dropbear 2012.55 from Debian wheezy's archive
# and stages dist/dropbear-armv7/{dropbear,dropbearkey,dropbearconvert,dbclient}

# 2. (optional) install your SSH public key for root.
cp ~/.ssh/id_rsa.pub dist/dropbear-armv7/authorized_keys

# 3. Build the package.
make package-with-dropbear DROPBEAR_DIR=dist/dropbear-armv7
```

Why these specific binaries: the ECU's userland is glibc 2.15 / armhf, the same generation as Debian wheezy. dropbear 2012.55 from wheezy's security pocket links cleanly. Newer dropbear builds (Debian bookworm and later) require a glibc that the ECU doesn't have.

If you'd rather use your own pre-built dropbear, skip step 1 and pass `DROPBEAR_DIR=` pointing at any directory with `dropbear` + `dropbearkey` (and optional `authorized_keys`).

The resulting `apsystems-sunspec-<version>-dropbear.tar.bz2` adds an `S98-dropbear` init script so the SSH daemon comes up automatically on each boot.

## Running as a sidecar

If you don't want to deploy on the ECU, run the binary anywhere with read access to the ECU's `/home` (NFS, rsync mirror, SSHFS) and `/tmp/parameters_app.conf`:

```sh
./ecu-sunspec \
    --bind tcp://0.0.0.0:1502 \
    --db-dir /mnt/ecu/home \
    --params-file /mnt/ecu/tmp/parameters_app.conf \
    --yuneng-dir /mnt/ecu/etc/yuneng
```

All configuration is via flags — see `--help`.

## Adding to Home Assistant

Use the [cgarwood/homeassistant-sunspec](https://github.com/cgarwood/homeassistant-sunspec) custom component. Add the integration up to four times:

```
Host: <ECU-IP>   Port: 1502   Slave ID: 1   →  System aggregate
Host: <ECU-IP>   Port: 1502   Slave ID: 2   →  Microinverter A
Host: <ECU-IP>   Port: 1502   Slave ID: 3   →  Microinverter B
Host: <ECU-IP>   Port: 1502   Slave ID: 4   →  Microinverter C
```

For the vendor model (model 64202 — daily/month/year energy aggregates, per-inverter RSSI, etc.) to be decoded natively, copy [`sunspec-models/model_64202.json`](sunspec-models/model_64202.json) into pysunspec2's `models/json/` directory inside the HA container. See [`sunspec-models/README.md`](sunspec-models/README.md) for the path.

## Adding to Victron Venus

```
Settings → PV inverters → Find PV inverters
```

Pick the entry that auto-discovers at `<ECU-IP>:1502` (the aggregate, slave ID 1). When prompted, choose **Position = AC out** (if your microinverters are downstream of the Multi for AC-coupled freq-shift control) and **Phase = L1** for single-phase setups.

If Venus' Fronius driver detection misbehaves after a binary upgrade — the standard fix is to toggle the inverter's Show-in-overview off and on again from the GX UI, which forces a driver reconnect.

## Architecture / details

- [`docs/EVENTS.md`](docs/EVENTS.md) — full APsystems event bitstring decode, with mapping to standard SunSpec Evt1 flags.
- [`sunspec-models/`](sunspec-models/) — vendor model 64202 JSON descriptor and instructions for plugging into pysunspec2 / generic SunSpec libraries.

The encoder is data-driven: the inverter list, panel count, AC topology, and curtailment caps all come from the ECU's runtime state — nothing is hardcoded to a specific site or fleet.

## License

MIT.
