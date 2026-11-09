package sunspec

import (
	"testing"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// Model 160 module n starts at body+8+n*20:
// ID, IDStr[8], DCA, DCV, DCW, DCWH(2), Tms(2), Tmp, DCSt, DCEvt(2).
func mpptModule(t *testing.T, bank Bank, n int) uint16 {
	t.Helper()
	base, ok := findModel(bank, MultiMPPTModelID)
	if !ok {
		t.Fatal("Model 160 missing")
	}
	return base + 2 + 8 + uint16(n)*20
}

// An offline inverter's panels read as "not implemented" — clients show
// "unknown" instead of failing the read on a vanished model.
func checkUnavailableModule(t *testing.T, bank Bank, n int) {
	t.Helper()
	m := mpptModule(t, bank, n)
	for off, name := range map[uint16]string{9: "DCA", 10: "DCV", 11: "DCW", 17: "DCSt"} {
		if got := bank.At(m + off); got != notImplU16 {
			t.Errorf("module %d %s=%#04x want not-impl", n, name, got)
		}
	}
	if got := bank.At(m + 16); got != notImplS16 {
		t.Errorf("module %d Tmp=%#04x want not-impl", n, got)
	}
	if bank.At(m+12) != 0 || bank.At(m+13) != 0 {
		t.Errorf("module %d DCWH want 0 (acc32 not-impl)", n)
	}
	if bank.At(m+18) != 0xFFFF || bank.At(m+19) != 0xFFFF {
		t.Errorf("module %d DCEvt want not-impl", n)
	}
}

func offlineQT2() source.Inverter {
	return source.Inverter{UID: "901500081373", Online: false, TypeCode: "02", Model: 41}
}

func TestEncode_Model160_OfflineInverterUnavailable(t *testing.T) {
	bank := Encode(source.Snapshot{Inverters: []source.Inverter{offlineQT2()}}, Options{})
	base, ok := findModel(bank, MultiMPPTModelID)
	if !ok {
		t.Fatal("Model 160 must stay present while the inverter is offline")
	}
	if got := bank.At(base + 2 + 6); got != 4 {
		t.Errorf("N=%d want 4 (the QT2's panels)", got)
	}
	for n := 0; n < 4; n++ {
		checkUnavailableModule(t, bank, n)
	}
	if got := readString(bank, mpptModule(t, bank, 3)+1, 8); got != "081373/D" {
		t.Errorf("IDStr=%q want 081373/D", got)
	}
}

// One inverter online, one offline: the online one's panels carry values,
// the offline one's are unavailable, and the module count covers both.
func TestEncode_Model160_MixedOnlineOffline(t *testing.T) {
	online := sampleInverter() // DS3, 2 panels
	offline := source.Inverter{UID: "999900000002", Online: false, TypeCode: "03"}
	bank := Encode(source.Snapshot{Inverters: []source.Inverter{online, offline}}, Options{})
	base, _ := findModel(bank, MultiMPPTModelID)
	if got := bank.At(base + 2 + 6); got != 6 {
		t.Fatalf("N=%d want 6 (2 + 4)", got)
	}
	if got := bank.At(mpptModule(t, bank, 0) + 11); got != 247 {
		t.Errorf("online panel A DCW=%d want 247", got)
	}
	for n := 2; n < 6; n++ {
		checkUnavailableModule(t, bank, n)
	}
}

func TestEncodePerInverter_Model160_OfflineUnavailable(t *testing.T) {
	bank := EncodePerInverter(offlineQT2(), "ECU", 2, Options{})
	for n := 0; n < 4; n++ {
		checkUnavailableModule(t, bank, n)
	}
}

// Day or night, the banks keep the same length, so every model keeps its
// address for clients that cached the layout (HA's register 40579 on
// unit 2 failed during radio misses on 2026-11-08/09).
func TestEncode_Model160_LayoutStableDayAndNight(t *testing.T) {
	day := sampleInverter()
	night := day
	night.Online = false
	agg := func(inv source.Inverter) int {
		return len(Encode(source.Snapshot{Inverters: []source.Inverter{inv}}, Options{}).Regs)
	}
	per := func(inv source.Inverter) int { return len(EncodePerInverter(inv, "ECU", 2, Options{}).Regs) }
	if agg(day) != agg(night) {
		t.Errorf("aggregate bank %d regs by day, %d by night", agg(day), agg(night))
	}
	if per(day) != per(night) {
		t.Errorf("per-inverter bank %d regs by day, %d by night", per(day), per(night))
	}
}
