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
)

// TestReverter_Direct exercises the Schedule / Cancel API in isolation.
func TestReverter_Direct(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	r := NewReverter(writer, nil)
	if r == nil {
		t.Fatal("NewReverter returned nil with non-nil writer")
	}

	// Cap INV-A first so we can observe restoration.
	if err := writer.SetMaxPower(context.Background(), "INV-A", 200); err != nil {
		t.Fatal(err)
	}

	r.Schedule(2, 80*time.Millisecond, []string{"INV-A"})
	if !r.pending(2) {
		t.Error("expected pending timer after Schedule")
	}

	time.Sleep(200 * time.Millisecond)
	if r.pending(2) {
		t.Error("timer should be cleared after firing")
	}

	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()
	var lp int
	raw.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != source.MaxPanelLimitW {
		t.Errorf("after reversion limitedpower=%d want %d", lp, source.MaxPanelLimitW)
	}
}

func TestReverter_CancelStopsTimer(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	r := NewReverter(writer, nil)

	if err := writer.SetMaxPower(context.Background(), "INV-A", 200); err != nil {
		t.Fatal(err)
	}

	r.Schedule(2, 80*time.Millisecond, []string{"INV-A"})
	r.Cancel(2)
	time.Sleep(200 * time.Millisecond)

	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()
	var lp int
	raw.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != 200 {
		t.Errorf("cancelled timer should leave cap intact, got limitedpower=%d", lp)
	}
}

func TestReverter_RescheduleResetsDeadline(t *testing.T) {
	_, writer := makeFixtureDB(t)
	defer writer.Close()

	r := NewReverter(writer, nil)
	r.Schedule(2, 200*time.Millisecond, []string{"INV-A"})
	time.Sleep(100 * time.Millisecond)
	// Reset before original deadline.
	r.Schedule(2, 200*time.Millisecond, []string{"INV-A"})
	time.Sleep(150 * time.Millisecond)
	if !r.pending(2) {
		t.Error("rescheduled timer should still be pending 150ms after reset")
	}
	r.Cancel(2)
}

// TestServer_RvrtTms_AutoReverts drives the full Modbus path: write
// WMaxLimPct=40 + Ena=1 + RvrtTms=1, wait 1.5s, verify cap was lifted.
func TestServer_RvrtTms_AutoReverts(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		ECUID: "999999999999",
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", Phase: 1,
				ACPowerW: 50, ACVoltageV: 230, RawTail: []int{130, 25, 230, 25, 230}},
		},
	}

	port := freePort(t)
	srv := New(fixedProvider{snap}, Config{
		URL:             "tcp://127.0.0.1:" + strconv.Itoa(port),
		RefreshInterval: time.Second,
		Writer:          writer,
		Writes:          config.Config{Writes: config.WritesConfig{Enabled: config.BoolPtr(true)}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	cli, _ := modbus.NewClient(&modbus.ClientConfiguration{
		URL: "tcp://127.0.0.1:" + strconv.Itoa(port), Timeout: 2 * time.Second,
	})
	cli.Open()
	defer cli.Close()
	cli.SetUnitId(2)

	ctlAddr, ok := walkForModel(cli, sunspec.ControlsModelID)
	if !ok {
		t.Fatal("Model 123 missing")
	}
	body := ctlAddr + 2

	// Write WMaxLimPct=40 + Ena=1 + RvrtTms=1 (1 second auto-revert).
	regs := []uint16{
		0,  // Conn_WinTms
		0,  // Conn_RvrtTms
		1,  // Conn (leave on)
		40, // WMaxLim_Pct
		0,  // WMaxLimPct_WinTms
		1,  // WMaxLimPct_RvrtTms = 1 second
		0,  // WMaxLimPct_RmpTms
		1,  // WMaxLim_Ena
	}
	if err := cli.WriteRegisters(body, regs); err != nil {
		t.Fatalf("write: %v", err)
	}

	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()

	// Immediately after write: cap is in place.
	var lp int
	raw.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != 200 {
		t.Errorf("after write limitedpower=%d want 200", lp)
	}

	// After RvrtTms (+ slack): cap is lifted.
	time.Sleep(1500 * time.Millisecond)
	raw.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != source.MaxPanelLimitW {
		t.Errorf("after RvrtTms limitedpower=%d want %d (auto-revert)",
			lp, source.MaxPanelLimitW)
	}
}

// TestServer_RvrtTms_RefreshKeepsCap simulates Victron's behavior of
// refreshing the cap before RvrtTms expires — the cap should persist.
func TestServer_RvrtTms_RefreshKeepsCap(t *testing.T) {
	dir, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", Phase: 1,
				ACPowerW: 50, ACVoltageV: 230, RawTail: []int{130, 25, 230, 25, 230}},
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
	body := ctlAddr + 2
	write := func() {
		cli.WriteRegisters(body, []uint16{0, 0, 1, 40, 0, 2, 0, 1})
	}

	write()
	time.Sleep(700 * time.Millisecond)
	write() // refresh — resets RvrtTms timer
	time.Sleep(700 * time.Millisecond)
	write() // refresh again

	// Total elapsed >= 2.1 s but each refresh resets the 2 s timer.
	raw, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer raw.Close()
	var lp int
	raw.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != 200 {
		t.Errorf("refresh-pattern should keep cap; got limitedpower=%d want 200", lp)
	}
}

// TestServer_RvrtTms_NotImplSentinel — RvrtTms = 0xFFFF (uint16 not-impl)
// must NOT schedule a reverter. Some clients send 0xFFFF as "I don't care."
func TestServer_RvrtTms_NotImplSentinel(t *testing.T) {
	_, writer := makeFixtureDB(t)
	defer writer.Close()

	snap := source.Snapshot{
		Inverters: []source.Inverter{
			{UID: "INV-A", Online: true, TypeCode: "01", Phase: 1,
				ACPowerW: 50, ACVoltageV: 230, RawTail: []int{130, 25, 230, 25, 230}},
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
	body := ctlAddr + 2
	cli.WriteRegisters(body, []uint16{0, 0, 1, 40, 0, 0xFFFF, 0, 1})

	if srv.reverter.pending(2) {
		t.Error("0xFFFF (not-impl sentinel) must not arm reverter")
	}
}
