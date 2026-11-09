package sunspec

import (
	"math"
	"testing"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// panelDCInverter: 4 channels, DC voltage known for the first two only.
func panelDCInverter() source.Inverter {
	inv := yc1000()
	inv.Model = 41
	inv.RawTail = []int{129, 154, 406, 159, 407, 151, 410, 153}
	inv.PanelDCVoltageV = []int{37, 38}
	return inv
}

// Model 160 module n starts at body+8+n*20: ID, IDStr[8], DCA, DCV, DCW.
func checkMPPTDC(t *testing.T, bank Bank) {
	t.Helper()
	base, ok := findModel(bank, MultiMPPTModelID)
	if !ok {
		t.Fatal("Model 160 missing")
	}
	mod := func(n int) uint16 { return base + 2 + 8 + uint16(n)*20 }

	// Channel A: 154 W at 37 V DC → 4.2 A (DCA_SF -1 → 42).
	if got := bank.At(mod(0) + 10); got != 37 {
		t.Errorf("A DCV=%d want 37", got)
	}
	if got := bank.At(mod(0) + 9); got != 42 {
		t.Errorf("A DCA=%d want 42", got)
	}
	// Channel C: no DC voltage → DCV and DCA not implemented, DCW kept.
	if got := bank.At(mod(2) + 10); got != notImplU16 {
		t.Errorf("C DCV=%#04x want not-impl", got)
	}
	if got := bank.At(mod(2) + 9); got != notImplU16 {
		t.Errorf("C DCA=%#04x want not-impl", got)
	}
	if got := bank.At(mod(2) + 11); got != 151 {
		t.Errorf("C DCW=%d want 151", got)
	}
}

// Model 114 body: DCV1..8, DCA1..8, DCW1..8, float32 each.
func checkDCDataFloat(t *testing.T, bank Bank) {
	t.Helper()
	base, ok := findModel(bank, DCDataFloatModelID)
	if !ok {
		t.Fatal("Model 114 missing")
	}
	body := base + 2
	if got := readFloat(bank, body); got != 37 {
		t.Errorf("DCV1=%v want 37", got)
	}
	if got := readFloat(bank, body+16); math.Abs(float64(got)-154.0/37) > 0.01 {
		t.Errorf("DCA1=%v want ≈4.16", got)
	}
	if got := readFloat(bank, body+4); !math.IsNaN(float64(got)) {
		t.Errorf("DCV3=%v want NaN", got)
	}
	if got := readFloat(bank, body+16+4); !math.IsNaN(float64(got)) {
		t.Errorf("DCA3=%v want NaN", got)
	}
	if got := readFloat(bank, body+32+4); got != 151 {
		t.Errorf("DCW3=%v want 151", got)
	}
}

func TestEncode_RealPanelDCVoltage(t *testing.T) {
	s := source.Snapshot{
		GridVoltageV:        235,
		InverterOnlineCount: 1,
		Inverters:           []source.Inverter{panelDCInverter()},
	}
	bank := Encode(s, Options{})
	checkMPPTDC(t, bank)
	checkDCDataFloat(t, bank)
}

func TestEncodePerInverter_RealPanelDCVoltage(t *testing.T) {
	bank := EncodePerInverter(panelDCInverter(), "ECU", 2, Options{})
	checkMPPTDC(t, bank)
	checkDCDataFloat(t, bank)
}
