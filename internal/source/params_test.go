package source

import (
	"strings"
	"testing"
)

func TestParseParamsApp_TypeOneAndThree(t *testing.T) {
	in := strings.NewReader(`01,3,20260501094000
999900000001,1,01,50.0,131,247,240,221,240
999900000002,1,03,50.1,126,238,232,235,156,218
999900000003,1,03,50.0,130,246,230,243,246,254
`)
	hdr, invs, err := ParseParamsApp(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if hdr.ProtocolVersion != "01" || hdr.InverterCount != 3 {
		t.Fatalf("hdr = %#v", hdr)
	}
	if len(invs) != 3 {
		t.Fatalf("invs = %d", len(invs))
	}

	// Type 01: DS3 2-channel.
	d := invs[0]
	if d.UID != "999900000001" || !d.Online || d.TypeCode != "01" {
		t.Errorf("inv[0] header: %#v", d)
	}
	if d.FrequencyHz != 50.0 {
		t.Errorf("inv[0] freq=%v want 50.0", d.FrequencyHz)
	}
	if d.TemperatureC != 31 {
		t.Errorf("inv[0] tempC=%d want 31", d.TemperatureC)
	}
	// AC heuristic: 247 (P0) + 221 (P1) = 468 W; voltage from col 6 = 240 V.
	if d.ACPowerW != 468 || d.ACVoltageV != 240 {
		t.Errorf("inv[0] AC=%dW@%dV want 468W@240V", d.ACPowerW, d.ACVoltageV)
	}
	if got, want := len(d.RawTail), 5; got != want {
		t.Errorf("inv[0] tail len=%d want %d", got, want)
	}

	// Type 03 (DS3-L): tail = [temp_raw, P0, V_ac, P1, P2, P3].
	// Sample: 999900000002,1,03,50.1,126,238,232,235,156,218
	//   tail = [126, 238, 232, 235, 156, 218]
	//   total W = 238 + 235 + 156 + 218 = 847
	//   V_ac = 232
	e := invs[1]
	if e.UID != "999900000002" || e.TypeCode != "03" || len(e.RawTail) != 6 {
		t.Errorf("inv[1]: %#v", e)
	}
	const wantE = 238 + 235 + 156 + 218
	if e.ACPowerW != wantE {
		t.Errorf("inv[1] ACPowerW=%d want %d (sum of 4 channel powers)", e.ACPowerW, wantE)
	}
	if e.ACVoltageV != 232 {
		t.Errorf("inv[1] ACVoltageV=%d want 232", e.ACVoltageV)
	}
}

func TestParseParamsApp_RejectsShortLine(t *testing.T) {
	in := strings.NewReader("01,1,20260501094000\n999900000001,1,01\n")
	_, _, err := ParseParamsApp(in)
	if err == nil {
		t.Fatal("expected error for short line")
	}
}

func TestLoadParamsFile_Fixture(t *testing.T) {
	hdr, invs, err := LoadParamsFile("../../testdata/ecu/parameters_app.conf")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if hdr.InverterCount != 3 {
		t.Errorf("count=%d want 3", hdr.InverterCount)
	}
	if len(invs) != 3 {
		t.Fatalf("invs=%d", len(invs))
	}
	for _, inv := range invs {
		if !inv.Online {
			t.Errorf("inv %s offline", inv.UID)
		}
		if inv.FrequencyHz < 49 || inv.FrequencyHz > 51 {
			t.Errorf("inv %s freq=%v out of grid range", inv.UID, inv.FrequencyHz)
		}
	}
}
