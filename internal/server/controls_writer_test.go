package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bolke/ecu-sunspec/internal/config"
	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
	"github.com/simonvetter/modbus"
	_ "modernc.org/sqlite"
)

// makeFixtureDB seeds a writable database.db with two inverters and the
// columns ControlsWriter touches. Returns the dir + ready Writer.
func makeFixtureDB(t *testing.T) (string, *source.Writer) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "database.db")
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE power(item INTEGER, id VARCHAR(15), limitedpower INTEGER, limitedresult INTEGER, stationarypower INTEGER, stationaryresult INTEGER, flag INTEGER)`,
		`CREATE TABLE turn_on_off(id VARCHAR(256), set_flag INTEGER, primary key(id))`,
		`INSERT INTO power VALUES(0,'INV-A',500,0,500,'-',0)`,
		`INSERT INTO power VALUES(1,'INV-B',500,0,500,'-',0)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	w, err := source.OpenWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, w
}

func TestServer_WriteWMaxLimPct_PerInverter(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		ECUID:        "999999999999",
		SystemPowerW: 100,
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", Phase: 1,
				ACPowerW: 50, ACVoltageV: 230, RawTail: []int{130, 25, 230, 25, 230}},
			{UID: "INV-B", Online: true, TypeCode: "01", Phase: 1,
				ACPowerW: 50, ACVoltageV: 230, RawTail: []int{130, 25, 230, 25, 230}},
		},
	}

	port := freePort(t)
	srv := New(fixedProvider{snap}, Config{
		URL:             "tcp://127.0.0.1:" + strconv.Itoa(port),
		RefreshInterval: time.Second,
		Writer:          writer,
		Writes: config.Config{Writes: config.WritesConfig{
			Enabled:   config.BoolPtr(true),
			AllowList: nil, // loopback always allowed
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	cli, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     "tcp://127.0.0.1:" + strconv.Itoa(port),
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Open(); err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// Walk the per-inverter bank for INV-A (uid 2) to find Model 123.
	cli.SetUnitId(2)
	ctlAddr, ok := walkForModel(cli, sunspec.ControlsModelID)
	if !ok {
		t.Fatal("Model 123 missing from per-inverter bank")
	}
	body := ctlAddr + 2

	// Write WMaxLim_Pct=40 + WMaxLim_Ena=1 atomically.
	regs := []uint16{
		0,  // Conn_WinTms
		0,  // Conn_RvrtTms
		1,  // Conn (leave on)
		40, // WMaxLim_Pct
		0, 0, 0,
		1, // WMaxLim_Ena
	}
	if err := cli.WriteRegisters(body, regs); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify via fresh DB read.
	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()
	var lp, flag int
	raw.QueryRow(`SELECT limitedpower, flag FROM power WHERE id='INV-A'`).Scan(&lp, &flag)
	if lp != 200 {
		t.Errorf("INV-A limitedpower=%d want 200 (40%% of 500)", lp)
	}
	if flag != 1 {
		t.Errorf("INV-A flag=%d want 1 (queued for main.exe)", flag)
	}

	// INV-B should be untouched (per-inverter write only hits INV-A).
	raw.QueryRow(`SELECT limitedpower, flag FROM power WHERE id='INV-B'`).Scan(&lp, &flag)
	if lp != 500 || flag != 0 {
		t.Errorf("INV-B should be untouched, got lp=%d flag=%d", lp, flag)
	}
}

func TestServer_WriteRejectedWhenDisabled(t *testing.T) {
	_, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", ACPowerW: 100, ACVoltageV: 230,
				RawTail: []int{130, 50, 230, 50, 230}},
		},
	}
	port := freePort(t)
	srv := New(fixedProvider{snap}, Config{
		URL:    "tcp://127.0.0.1:" + strconv.Itoa(port),
		Writer: writer,
		Writes: config.Config{Writes: config.WritesConfig{Enabled: config.BoolPtr(false)}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	cli, _ := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     "tcp://127.0.0.1:" + strconv.Itoa(port),
		Timeout: 2 * time.Second,
	})
	cli.Open()
	defer cli.Close()
	cli.SetUnitId(2)

	ctlAddr, _ := walkForModel(cli, sunspec.ControlsModelID)
	if err := cli.WriteRegister(ctlAddr+2+sunspec.OffControlsWMaxLimPct, 50); err == nil {
		t.Error("expected write to fail (writes disabled), got nil error")
	}
}

func TestServer_WriteConn_TurnsOff(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", ACPowerW: 100, ACVoltageV: 230,
				RawTail: []int{130, 50, 230, 50, 230}},
		},
	}
	port := freePort(t)
	srv := New(fixedProvider{snap}, Config{
		URL:    "tcp://127.0.0.1:" + strconv.Itoa(port),
		Writer: writer,
		Writes: config.Config{Writes: config.WritesConfig{Enabled: config.BoolPtr(true)}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.Start(ctx)
	defer srv.Stop()

	cli, _ := modbus.NewClient(&modbus.ClientConfiguration{
		URL: "tcp://127.0.0.1:" + strconv.Itoa(port), Timeout: 2 * time.Second,
	})
	cli.Open()
	defer cli.Close()
	cli.SetUnitId(2)

	ctlAddr, _ := walkForModel(cli, sunspec.ControlsModelID)
	if err := cli.WriteRegister(ctlAddr+2+sunspec.OffControlsConn, 0); err != nil {
		t.Fatal(err)
	}

	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()
	var state int
	raw.QueryRow(`SELECT set_flag FROM turn_on_off WHERE id='INV-A'`).Scan(&state)
	if state != 0 {
		t.Errorf("turn_on_off.set_flag=%d want 0 (off)", state)
	}
}

// walkForModel scans the standard SunSpec bank starting at 40000 looking for
// the given model ID. Returns the absolute address of the model's ID register
// (i.e., body starts at +2).
func walkForModel(cli *modbus.ModbusClient, id uint16) (uint16, bool) {
	addr := uint16(sunspec.BaseRegister + 2) // skip "SunS"
	for hop := 0; hop < 50; hop++ {
		hdr, err := cli.ReadRegisters(addr, 2, modbus.HOLDING_REGISTER)
		if err != nil {
			return 0, false
		}
		if hdr[0] == sunspec.EndModelID {
			return 0, false
		}
		if hdr[0] == id {
			return addr, true
		}
		addr += 2 + hdr[1]
	}
	return 0, false
}
