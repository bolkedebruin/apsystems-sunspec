package sunspec

import (
	"testing"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// Model 64203 body: FastPoll, FastPollRemS, LastPollTms (2 regs), DataAgeS, Pad.
func TestEncode_ECUControl_ServiceModeActive(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 37, 25, 0, time.Local)
	last := time.Date(2026, 11, 7, 12, 37, 0, 0, time.Local)
	s := source.Snapshot{
		Captured:         now,
		LastPollTime:     last,
		FastPolling:      true,
		FastPollingSince: now.Add(-10 * time.Minute),
	}
	bank := Encode(s, Options{})

	base, ok := findModel(bank, ECUControlModelID)
	if !ok {
		t.Fatal("Model 64203 missing from the aggregate bank")
	}
	if got := bank.At(base + 1); got != ECUControlBodyLen {
		t.Errorf("L=%d want %d", got, ECUControlBodyLen)
	}
	body := base + 2
	if got := bank.At(body); got != 1 {
		t.Errorf("FastPoll=%d want 1", got)
	}
	// Entered 10 min ago; counts up from activation.
	if got := bank.At(body + 1); got != 600 {
		t.Errorf("FastPollElapsedS=%d want 600", got)
	}
	tms := uint32(bank.At(body+2))<<16 | uint32(bank.At(body+3))
	if tms != uint32(last.Unix()) {
		t.Errorf("LastPollTms=%d want %d", tms, last.Unix())
	}
	if got := bank.At(body + 4); got != 25 {
		t.Errorf("DataAgeS=%d want 25", got)
	}
}

// body[5..13]: EmaConnected, LastProdRecTms (2), ProdRecPending,
// ProdRecLastHour, LastEmaCtrlTms (2), ProdRecAgeS, Pad.
func TestEncode_ECUControl_EMAStatus(t *testing.T) {
	now := time.Date(2026, 11, 7, 13, 36, 0, 0, time.Local)
	lastRec := time.Date(2026, 11, 7, 13, 35, 0, 0, time.Local)
	lastCtl := time.Date(2026, 11, 7, 13, 27, 35, 0, time.Local)
	s := source.Snapshot{
		Captured: now,
		EMA: source.EMAStatus{
			Connected:           source.EMAConnected,
			LastProdRecord:      lastRec,
			ProdRecordsPending:  1,
			ProdRecordsLastHour: 7,
			LastControlContact:  lastCtl,
		},
	}
	bank := Encode(s, Options{})
	base, ok := findModel(bank, ECUControlModelID)
	if !ok {
		t.Fatal("Model 64203 missing")
	}
	body := base + 2
	u32 := func(off uint16) uint32 { return uint32(bank.At(body+off))<<16 | uint32(bank.At(body+off+1)) }

	if got := bank.At(body + 5); got != 1 {
		t.Errorf("EmaConnected=%d want 1", got)
	}
	if got := u32(6); got != uint32(lastRec.Unix()) {
		t.Errorf("LastProdRecTms=%d want %d", got, lastRec.Unix())
	}
	if got := bank.At(body + 8); got != 1 {
		t.Errorf("ProdRecPending=%d want 1", got)
	}
	if got := bank.At(body + 9); got != 7 {
		t.Errorf("ProdRecLastHour=%d want 7", got)
	}
	if got := u32(10); got != uint32(lastCtl.Unix()) {
		t.Errorf("LastEmaCtrlTms=%d want %d", got, lastCtl.Unix())
	}
	if got := bank.At(body + 12); got != 60 {
		t.Errorf("ProdRecAgeS=%d want 60", got)
	}
}

func TestEncode_ECUControl_NormalModeAndUnknownPoll(t *testing.T) {
	bank := Encode(source.Snapshot{Captured: time.Now()}, Options{})
	base, ok := findModel(bank, ECUControlModelID)
	if !ok {
		t.Fatal("Model 64203 missing")
	}
	body := base + 2
	if got := bank.At(body); got != 0 {
		t.Errorf("FastPoll=%d want 0", got)
	}
	if got := bank.At(body + 1); got != 0 {
		t.Errorf("FastPollElapsedS=%d want 0 in normal mode", got)
	}
	if bank.At(body+2) != 0xFFFF || bank.At(body+3) != 0xFFFF {
		t.Errorf("LastPollTms=%#04x%04x want not-impl", bank.At(body+2), bank.At(body+3))
	}
	if got := bank.At(body + 4); got != notImplU16 {
		t.Errorf("DataAgeS=%#04x want not-impl", got)
	}
	// No EMA information at all: unknown connection, no records seen.
	if got := bank.At(body + 5); got != notImplU16 {
		t.Errorf("EmaConnected=%#04x want not-impl", got)
	}
	if bank.At(body+6) != 0xFFFF || bank.At(body+7) != 0xFFFF {
		t.Error("LastProdRecTms want not-impl")
	}
	if bank.At(body+10) != 0xFFFF || bank.At(body+11) != 0xFFFF {
		t.Error("LastEmaCtrlTms want not-impl")
	}
	if got := bank.At(body + 12); got != notImplU16 {
		t.Errorf("ProdRecAgeS=%#04x want not-impl", got)
	}
}

// A Service Mode forgotten for days saturates instead of wrapping.
func TestEncode_ECUControl_ElapsedSaturates(t *testing.T) {
	now := time.Now()
	s := source.Snapshot{Captured: now, FastPolling: true, FastPollingSince: now.Add(-48 * time.Hour)}
	bank := Encode(s, Options{})
	base, _ := findModel(bank, ECUControlModelID)
	if got := bank.At(base + 2 + 1); got != 65534 {
		t.Errorf("FastPollElapsedS=%d want 65534 (saturated)", got)
	}
}

// Home Assistant's Modbus integration addresses FastPoll by a fixed
// register, so Model 64203 must sit ahead of Model 160, which only
// exists while panels report (day vs night).
func TestEncode_ECUControl_AddressStableDayAndNight(t *testing.T) {
	day := sampleInverter()
	night := sampleInverter()
	night.Online = false
	addr := func(inv source.Inverter) uint16 {
		bank := Encode(source.Snapshot{Inverters: []source.Inverter{inv}}, Options{})
		base, ok := findModel(bank, ECUControlModelID)
		if !ok {
			t.Fatal("Model 64203 missing")
		}
		return base
	}
	if d, n := addr(day), addr(night); d != n {
		t.Errorf("Model 64203 at %d by day, %d by night; want a fixed address", d, n)
	}
}

// With the adapter's FastPoll choice known, FastPoll reads back the
// configured mode (2 = auto) and body[13] the auto-mode threshold, while
// FastPollElapsedS keeps telling whether Service Mode is on right now.
func TestEncode_ECUControl_ConfiguredMode(t *testing.T) {
	now := time.Now()
	s := source.Snapshot{
		Captured:           now,
		FastPolling:        true,
		FastPollingSince:   now.Add(-90 * time.Second),
		FastPollConfigured: true,
		FastPollMode:       2,
		FastPollThresholdW: 50,
	}
	bank := Encode(s, Options{})
	base, _ := findModel(bank, ECUControlModelID)
	body := base + 2
	if got := bank.At(body + ECUControlBodyFastPollOff); got != 2 {
		t.Errorf("FastPoll=%d want 2 (auto)", got)
	}
	if got := bank.At(body + 1); got != 90 {
		t.Errorf("FastPollElapsedS=%d want 90", got)
	}
	if got := bank.At(body + ECUControlBodyThresholdOff); got != 50 {
		t.Errorf("FastPollThresholdW=%d want 50", got)
	}
}

func TestEncodePerInverter_NoECUControl(t *testing.T) {
	bank := EncodePerInverter(sampleInverter(), "ECU", 2, Options{})
	if _, ok := findModel(bank, ECUControlModelID); ok {
		t.Error("Model 64203 is ECU-wide and belongs on unit 1 only")
	}
}
