package sunspec

import (
	"math"
	"testing"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// yc1000 is the live sample from an ECU-C running a single YC1000:
// 751 W over four channels, line-to-line 406/407/410 V (≈235 V line-neutral).
func yc1000() source.Inverter {
	return source.Inverter{
		UID:          "901500081373",
		Online:       true,
		TypeCode:     "02",
		Phase:        1,
		ACPowerW:     751,
		ACVoltageV:   235,
		FrequencyHz:  50.0,
		TemperatureC: 29,
		RawTail:      []int{129, 187, 406, 194, 407, 184, 410, 186},
	}
}

func readFloat(bank Bank, addr uint16) float32 {
	return math.Float32frombits(uint32(bank.At(addr))<<16 | uint32(bank.At(addr+1)))
}

// checkThreePhaseInt asserts a Model 103 body: 751 W split evenly over
// three phases at 235 V → 1.07 A per phase (0.1 A units: 11), 3.2 A total,
// and the measured line-to-line voltages in PPVphAB/BC/CA.
func checkThreePhaseInt(t *testing.T, bank Bank) {
	t.Helper()
	base, ok := findModel(bank, InverterModelThreePhase)
	if !ok {
		t.Fatal("Model 103 missing")
	}
	if _, ok := findModel(bank, InverterModelSinglePhase); ok {
		t.Error("Model 101 should not be emitted alongside 103")
	}
	body := base + 2
	if got := bank.At(body); got != 32 {
		t.Errorf("A=%d want 32 (3.2 A total)", got)
	}
	for i, name := range []string{"AphA", "AphB", "AphC"} {
		if got := bank.At(body + 1 + uint16(i)); got != 11 {
			t.Errorf("%s=%d want 11 (1.1 A)", name, got)
		}
	}
	for i, want := range []uint16{406, 407, 410} {
		if got := bank.At(body + 5 + uint16(i)); got != want {
			t.Errorf("PPV[%d]=%d want %d", i, got, want)
		}
	}
	for i := uint16(0); i < 3; i++ {
		if got := bank.At(body + 8 + i); got != 235 {
			t.Errorf("PhV[%d]=%d want 235", i, got)
		}
	}
}

func checkThreePhaseFloat(t *testing.T, bank Bank) {
	t.Helper()
	base, ok := findModel(bank, InverterModelThreePhaseFloat)
	if !ok {
		t.Fatal("Model 113 missing")
	}
	body := base + 2
	if got := readFloat(bank, body+2); math.Abs(float64(got)-751.0/3/235) > 0.06 {
		t.Errorf("AphA=%v want ≈1.07", got)
	}
	if got := readFloat(bank, body+6); math.Abs(float64(got)-751.0/3/235) > 0.06 {
		t.Errorf("AphC=%v want ≈1.07", got)
	}
	for i, want := range []float32{406, 407, 410} {
		if got := readFloat(bank, body+8+uint16(i)*2); got != want {
			t.Errorf("PPV[%d]=%v want %v", i, got, want)
		}
	}
	if got := readFloat(bank, body+18); got != 235 {
		t.Errorf("PhVphC=%v want 235", got)
	}
}

func TestEncode_YC1000IsThreePhase(t *testing.T) {
	s := source.Snapshot{
		GridVoltageV:        235,
		SystemPowerW:        751,
		InverterOnlineCount: 1,
		Inverters:           []source.Inverter{yc1000()},
	}
	bank := Encode(s, Options{})
	checkThreePhaseInt(t, bank)
	checkThreePhaseFloat(t, bank)
}

func TestEncodePerInverter_YC1000IsThreePhase(t *testing.T) {
	bank := EncodePerInverter(yc1000(), "215000054677", 2, Options{})
	checkThreePhaseInt(t, bank)
	checkThreePhaseFloat(t, bank)
}

// Single-phase micros spread across phases keep the existing behavior:
// no line-to-line voltages, all of an inverter's power on its own phase.
func TestEncode_SinglePhaseMicrosKeepNoLineVoltages(t *testing.T) {
	s := source.Snapshot{GridVoltageV: 230}
	for p := 1; p <= 3; p++ {
		s.Inverters = append(s.Inverters, source.Inverter{Online: true, TypeCode: "01", Phase: p, ACPowerW: 230})
	}
	bank := Encode(s, Options{})
	base, ok := findModel(bank, InverterModelThreePhase)
	if !ok {
		t.Fatal("Model 103 missing")
	}
	for i := uint16(0); i < 3; i++ {
		if got := bank.At(base + 2 + 5 + i); got != notImplU16 {
			t.Errorf("PPV[%d]=%#04x want not-impl", i, got)
		}
		if got := bank.At(base + 2 + 1 + i); got != 10 {
			t.Errorf("Aph[%d]=%d want 10 (1.0 A)", i, got)
		}
	}
}
