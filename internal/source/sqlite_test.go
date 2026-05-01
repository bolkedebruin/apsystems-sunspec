package source

import (
	"context"
	"testing"
)

func TestSQLiteReader_Fixtures(t *testing.T) {
	r, err := OpenSQLite("../../testdata/ecu")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	ctx := context.Background()

	kwh, err := r.LifetimeEnergyKWh(ctx)
	if err != nil {
		t.Fatalf("lifetime: %v", err)
	}
	if kwh < 1000 {
		t.Errorf("lifetime kWh too low: %v", kwh)
	}

	today, err := r.TodayEnergyKWh(ctx)
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if today < 0 {
		t.Errorf("today negative: %v", today)
	}

	w, err := r.LatestSystemPowerW(ctx)
	if err != nil {
		t.Fatalf("system power: %v", err)
	}
	if w < 0 || w > 50000 {
		t.Errorf("system power out of range: %v", w)
	}

	list, err := r.InverterList(ctx)
	if err != nil {
		t.Fatalf("inverter list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("no inverters")
	}

	sigs, err := r.SignalStrengths(ctx)
	if err != nil {
		t.Fatalf("signal: %v", err)
	}
	if len(sigs) == 0 {
		t.Error("no signal strengths")
	}
}

func TestBuilder_Snapshot(t *testing.T) {
	r, err := OpenSQLite("../../testdata/ecu")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	b := NewBuilder(r, "../../testdata/ecu/parameters_app.conf", "")
	snap, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if snap.LifetimeEnergyWh < 1_000_000 {
		t.Errorf("lifetime Wh too low: %d", snap.LifetimeEnergyWh)
	}
	if snap.InverterCount != 3 {
		t.Errorf("inverter count=%d want 3", snap.InverterCount)
	}
	if snap.GridFrequencyHz < 49.5 || snap.GridFrequencyHz > 50.5 {
		t.Errorf("freq out of grid range: %v", snap.GridFrequencyHz)
	}
	if snap.GridVoltageV < 200 || snap.GridVoltageV > 260 {
		t.Errorf("voltage out of grid range: %v", snap.GridVoltageV)
	}
	if snap.SystemPowerW <= 0 {
		t.Errorf("system power=%d expected positive (sun-up fixture)", snap.SystemPowerW)
	}

	// The snapshot should sum live params: 247+221 + 4×238 + 4×246 = …
	// Independently re-compute the total from the params parse and confirm
	// our builder agrees within rounding.
	_, invs, err := LoadParamsFile("../../testdata/ecu/parameters_app.conf")
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	var sum int
	for _, inv := range invs {
		if inv.Online {
			sum += inv.ACPowerW
		}
	}
	if int32(sum) != snap.SystemPowerW {
		t.Errorf("snapshot SystemPowerW=%d does not match params sum %d",
			snap.SystemPowerW, sum)
	}
}
