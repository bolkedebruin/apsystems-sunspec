package source

import (
	"context"
	"testing"
)

func TestPerInverterProtection_Fixture(t *testing.T) {
	r, err := OpenSQLite("../../testdata/ecu")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	got, err := r.PerInverterProtection(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 inverters in protection_parameters60code, got %d", len(got))
	}

	// Inverter 999900000001 mirrors the live DS3-L: AC=195, AQ=183, AY=259,
	// AD=263, AE=47.5, AF=52.0, AJ=47.0, AK=52.0, AG=60.
	ds3l, ok := got["999900000001"]
	if !ok {
		t.Fatal("missing 999900000001")
	}
	if ds3l.UVStg2 != 195 || !ds3l.Has["AC"] {
		t.Errorf("DS3-L UVStg2 (AC) = %v has=%v want 195/true", ds3l.UVStg2, ds3l.Has["AC"])
	}
	if ds3l.UVFast != 183 {
		t.Errorf("DS3-L UVFast (AQ) = %v want 183", ds3l.UVFast)
	}
	if ds3l.OVStg3 != 259 {
		t.Errorf("DS3-L OVStg3 (AY) = %v want 259", ds3l.OVStg3)
	}
	if ds3l.OFFast != 52.0 {
		t.Errorf("DS3-L OFFast (AK) = %v want 52.0", ds3l.OFFast)
	}
	if ds3l.ReconnectS != 60 {
		t.Errorf("DS3-L ReconnectS (AG) = %v want 60", ds3l.ReconnectS)
	}
	if ds3l.ReconnFHi != 50.2 {
		t.Errorf("DS3-L ReconnFHi (BQ) = %v want 50.2", ds3l.ReconnFHi)
	}

	// 999900000002 mirrors a live DS3: AK=51.5, BK=0.12, AG=30.
	ds3, ok := got["999900000002"]
	if !ok {
		t.Fatal("missing 999900000002")
	}
	if ds3.OFFast != 51.5 {
		t.Errorf("DS3 OFFast (AK) = %v want 51.5", ds3.OFFast)
	}
	if ds3.OF2ClrS != 0.12 {
		t.Errorf("DS3 OF2ClrS (BK) = %v want 0.12", ds3.OF2ClrS)
	}
	if ds3.ReconnectS != 30 {
		t.Errorf("DS3 ReconnectS (AG) = %v want 30", ds3.ReconnectS)
	}
}

func TestPerInverterProtection_Snapshot(t *testing.T) {
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
	if len(snap.Protection) != 3 {
		t.Fatalf("snapshot protection map size = %d want 3", len(snap.Protection))
	}
}
