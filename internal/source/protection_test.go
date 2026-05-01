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

	// Inverter 999900000001 mirrors the live DS3 (UID 704...): AC=195, AQ=183,
	// AY=259, AD=263, AE=47.5, AF=52.0, AJ=47.0, AK=52.0, AG=60. This DS3 has
	// looser OF protection (52.0 Hz) than the type-03 inverters in the fleet.
	ds3, ok := got["999900000001"]
	if !ok {
		t.Fatal("missing 999900000001")
	}
	if ds3.UVStg2 != 195 || !ds3.Has["AC"] {
		t.Errorf("DS3 UVStg2 (AC) = %v has=%v want 195/true", ds3.UVStg2, ds3.Has["AC"])
	}
	if ds3.UVFast != 183 {
		t.Errorf("DS3 UVFast (AQ) = %v want 183", ds3.UVFast)
	}
	if ds3.OVStg3 != 259 {
		t.Errorf("DS3 OVStg3 (AY) = %v want 259", ds3.OVStg3)
	}
	if ds3.OFFast != 52.0 {
		t.Errorf("DS3 OFFast (AK) = %v want 52.0", ds3.OFFast)
	}
	if ds3.ReconnectS != 60 {
		t.Errorf("DS3 ReconnectS (AG) = %v want 60", ds3.ReconnectS)
	}
	if ds3.ReconnFHi != 50.2 {
		t.Errorf("DS3 ReconnFHi (BQ) = %v want 50.2", ds3.ReconnFHi)
	}

	// 999900000002 mirrors a live type-03 inverter (UID 806...) with stricter
	// OF protection: AK=51.5, BK=0.12, AG=30.
	qs1, ok := got["999900000002"]
	if !ok {
		t.Fatal("missing 999900000002")
	}
	if qs1.OFFast != 51.5 {
		t.Errorf("type-03 OFFast (AK) = %v want 51.5", qs1.OFFast)
	}
	if qs1.OF2ClrS != 0.12 {
		t.Errorf("type-03 OF2ClrS (BK) = %v want 0.12", qs1.OF2ClrS)
	}
	if qs1.ReconnectS != 30 {
		t.Errorf("type-03 ReconnectS (AG) = %v want 30", qs1.ReconnectS)
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
