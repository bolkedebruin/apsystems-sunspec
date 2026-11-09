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

	// Type 03 (QS1, 4-channel): tail = [temp_raw, P0, V_ac, P1, P2, P3].
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

// Type 02 (YC1000, 4-channel three-phase) sampled live on an ECU-C
// (fw C1.2.27A): tail = [temp_raw, P0, V_AB, P1, V_BC, P2, V_CA, P3].
// The three voltages are line-to-line (~400 V grid); ACVoltageV is the
// line-neutral equivalent so it lines up with the 230 V-based protection
// thresholds and the single-phase inverters.
func TestParseParamsApp_TypeTwo(t *testing.T) {
	in := strings.NewReader(`01,1,20261101151000
901500081373,1,02,50.0,129,187,406,194,407,184,410,186
`)
	_, invs, err := ParseParamsApp(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	inv := invs[0]
	if inv.TemperatureC != 29 {
		t.Errorf("tempC=%d want 29", inv.TemperatureC)
	}
	const wantW = 187 + 194 + 184 + 186
	if inv.ACPowerW != wantW {
		t.Errorf("ACPowerW=%d want %d (sum of 4 channel powers)", inv.ACPowerW, wantW)
	}
	// mean(406, 407, 410) / √3 = 235.4 → 235 V.
	if inv.ACVoltageV != 235 {
		t.Errorf("ACVoltageV=%d want 235 (line-neutral)", inv.ACVoltageV)
	}
	if got := inv.PanelCount(); got != 4 {
		t.Errorf("PanelCount=%d want 4", got)
	}
	if got, want := inv.PanelPowers(), []int{187, 194, 184, 186}; !equalInts(got, want) {
		t.Errorf("PanelPowers=%v want %v", got, want)
	}
	// Type 02 covers both the YC1000 and the 2000 W QT2, so there's no
	// safe TypeCode fallback: the rating comes from the model table.
	if got := inv.NameplateW(); got != 0 {
		t.Errorf("NameplateW=%d want 0 without a model table entry", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInverter_TypeTwoIsThreePhaseWithLineVoltages(t *testing.T) {
	inv := Inverter{TypeCode: "02", RawTail: []int{129, 187, 406, 194, 407, 184, 410, 186}}
	if !inv.ThreePhase() {
		t.Error("YC1000 (type 02) should be three-phase")
	}
	if got, want := inv.LineToLineV(), [3]int{406, 407, 410}; got != want {
		t.Errorf("LineToLineV=%v want %v", got, want)
	}

	ds3 := Inverter{TypeCode: "01", RawTail: []int{131, 247, 240, 221, 240}}
	if ds3.ThreePhase() {
		t.Error("DS3 (type 01) should not be three-phase")
	}
	if got := ds3.LineToLineV(); got != [3]int{} {
		t.Errorf("DS3 LineToLineV=%v want zeros", got)
	}
}

// The shipped table must rate model 41 (QT2, reported as type 02 by
// ECU-C fw C1.2.27A) at its 2000 W AC nameplate.
func TestPackagedNameplateTable_QT2(t *testing.T) {
	saved := nameplateByModel
	t.Cleanup(func() { nameplateByModel = saved })

	if _, err := LoadNameplateTable("../../packaging/nameplate.json"); err != nil {
		t.Fatalf("load: %v", err)
	}
	inv := Inverter{TypeCode: "02", Model: 41}
	if got := inv.NameplateW(); got != 2000 {
		t.Errorf("model 41 NameplateW=%d want 2000", got)
	}
}
