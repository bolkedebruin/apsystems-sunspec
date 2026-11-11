package server

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
)

func newKeeper(t *testing.T) (*FastPollKeeper, string, string) {
	t.Helper()
	dir := t.TempDir()
	flag := filepath.Join(dir, "fast_polling.conf")
	state := filepath.Join(dir, "sunspec-fastpoll.conf")
	return NewFastPollKeeper(flag, state, log.New(io.Discard, "", 0)), flag, state
}

func reload(flag, state string) *FastPollKeeper {
	return NewFastPollKeeper(flag, state, log.New(io.Discard, "", 0))
}

func flagOn(flag string) bool {
	on, _ := source.ReadFastPolling(flag)
	return on
}

// Enabling over Modbus writes the ECU flag and remembers the choice.
func TestFastPollKeeper_SetModePersists(t *testing.T) {
	k, flag, state := newKeeper(t)
	if err := k.SetMode(FastPollOn); err != nil {
		t.Fatal(err)
	}
	if !flagOn(flag) {
		t.Error("ECU flag not set")
	}
	if got := reload(flag, state).Mode(); got != FastPollOn {
		t.Errorf("reloaded mode=%d want %d", got, FastPollOn)
	}

	if err := k.SetMode(FastPollOff); err != nil {
		t.Fatal(err)
	}
	if flagOn(flag) {
		t.Error("ECU flag not cleared")
	}
	if got := reload(flag, state).Mode(); got != FastPollOff {
		t.Errorf("reloaded mode=%d want off", got)
	}
}

// A state file written by the previous version ("1") still loads.
func TestFastPollKeeper_LoadsLegacyState(t *testing.T) {
	_, flag, state := newKeeper(t)
	os.WriteFile(state, []byte("1\n"), 0o644)
	k := reload(flag, state)
	if k.Mode() != FastPollOn || k.ThresholdW() != DefaultFastPollThresholdW {
		t.Errorf("mode=%d threshold=%d", k.Mode(), k.ThresholdW())
	}
}

func TestFastPollKeeper_ThresholdPersists(t *testing.T) {
	k, flag, state := newKeeper(t)
	if k.ThresholdW() != 50 {
		t.Errorf("default threshold=%d want 50", k.ThresholdW())
	}
	if err := k.SetThresholdW(120); err != nil {
		t.Fatal(err)
	}
	if got := reload(flag, state).ThresholdW(); got != 120 {
		t.Errorf("reloaded threshold=%d want 120", got)
	}
	if err := k.SetThresholdW(0); err == nil {
		t.Error("threshold 0 accepted")
	}
}

// main.exe writes "0" after 60 minutes; in "on" mode the keeper puts "1" back.
func TestFastPollKeeper_ReassertsAfterECUReset(t *testing.T) {
	k, flag, _ := newKeeper(t)
	now := time.Date(2026, 11, 7, 14, 0, 0, 0, time.Local)
	k.SetMode(FastPollOn)
	os.WriteFile(flag, []byte("0"), 0o644)
	k.Tick(now, 0)
	if !flagOn(flag) {
		t.Error("Service Mode not re-asserted")
	}
}

// In "off" mode the keeper never touches the flag, so the EMA app or
// remote control keep their say.
func TestFastPollKeeper_OffLeavesFlagAlone(t *testing.T) {
	k, flag, _ := newKeeper(t)
	os.WriteFile(flag, []byte("1"), 0o644)
	k.Tick(time.Now(), 1000)
	if b, _ := os.ReadFile(flag); string(b) != "1" {
		t.Errorf("flag rewritten to %q in off mode", b)
	}
}

// The flag lives on flash: at most one re-assert per minute.
func TestFastPollKeeper_RateLimited(t *testing.T) {
	k, flag, _ := newKeeper(t)
	now := time.Date(2026, 11, 7, 14, 0, 0, 0, time.Local)
	k.SetMode(FastPollOn)
	os.WriteFile(flag, []byte("0"), 0o644)
	k.Tick(now, 0)
	os.WriteFile(flag, []byte("0"), 0o644)
	k.Tick(now.Add(30*time.Second), 0)
	if flagOn(flag) {
		t.Error("re-asserted twice within a minute")
	}
	k.Tick(now.Add(61*time.Second), 0)
	if !flagOn(flag) {
		t.Error("not re-asserted after the rate-limit window")
	}
}

func TestFastPollKeeper_RestoresAfterRestart(t *testing.T) {
	k, flag, state := newKeeper(t)
	k.SetMode(FastPollOn)
	os.Remove(flag)
	reload(flag, state).Tick(time.Now(), 0)
	if !flagOn(flag) {
		t.Error("Service Mode not restored after restart")
	}
}

// Auto mode: Service Mode while production is at or above the threshold,
// normal mode once it drops below threshold − max(10 W, 20 %), with at
// least 10 minutes between switches.
func TestFastPollKeeper_Auto(t *testing.T) {
	k, flag, _ := newKeeper(t)
	t0 := time.Date(2026, 11, 8, 8, 30, 0, 0, time.Local)
	if err := k.SetMode(FastPollAuto); err != nil {
		t.Fatal(err)
	}
	if flagOn(flag) {
		t.Error("auto mode switched Service Mode on before any production reading")
	}

	k.Tick(t0, 20) // dawn
	if flagOn(flag) {
		t.Error("Service Mode on at 20 W (threshold 50)")
	}
	k.Tick(t0.Add(5*time.Minute), 55)
	if !flagOn(flag) {
		t.Error("Service Mode off at 55 W")
	}
	// 45 W is inside the hysteresis band (off below 40 W).
	k.Tick(t0.Add(20*time.Minute), 45)
	if !flagOn(flag) {
		t.Error("switched off inside the hysteresis band")
	}
	// Below the band: switches off only once it has stayed there for
	// 2 minutes.
	k.Tick(t0.Add(21*time.Minute), 30)
	if !flagOn(flag) {
		t.Error("switched off on the first reading below the band")
	}
	k.Tick(t0.Add(23*time.Minute), 30)
	if flagOn(flag) {
		t.Error("Service Mode still on after 2 minutes at 30 W")
	}
	// Back up 3 minutes later: within the 10-minute dwell, stays off.
	k.Tick(t0.Add(26*time.Minute), 80)
	if flagOn(flag) {
		t.Error("switched back on within the 10-minute dwell")
	}
	k.Tick(t0.Add(34*time.Minute), 80)
	if !flagOn(flag) {
		t.Error("not switched back on after the dwell")
	}

	// main.exe's 60-minute exit while still above the threshold: re-enabled
	// right away, not held back by the dwell.
	os.WriteFile(flag, []byte("0"), 0o644)
	k.Tick(t0.Add(35*time.Minute), 80)
	if !flagOn(flag) {
		t.Error("auto mode did not re-enable after main.exe's reset")
	}
}

// 2026-11-08: a single 0 W reading at 13:15 in the middle of ~370 W of
// production switched Service Mode off for 10 minutes. A dip shorter than
// 2 minutes must not.
func TestFastPollKeeper_AutoIgnoresShortDip(t *testing.T) {
	k, flag, _ := newKeeper(t)
	t0 := time.Date(2026, 11, 8, 13, 0, 0, 0, time.Local)
	k.SetMode(FastPollAuto)
	k.Tick(t0, 370)
	for i, w := range []int{0, 365, 0, 0, 370} { // every 20 s
		k.Tick(t0.Add(time.Duration(i+1)*20*time.Second+15*time.Minute), w)
		if !flagOn(flag) {
			t.Fatalf("switched off on reading %d (%d W)", i, w)
		}
	}
}

func TestFastPollKeeper_AutoUsesThreshold(t *testing.T) {
	k, flag, _ := newKeeper(t)
	t0 := time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)
	k.SetThresholdW(200)
	k.SetMode(FastPollAuto)
	k.Tick(t0, 150)
	if flagOn(flag) {
		t.Error("Service Mode on at 150 W with a 200 W threshold")
	}
	k.Tick(t0.Add(time.Minute), 210)
	if !flagOn(flag) {
		t.Error("Service Mode off at 210 W with a 200 W threshold")
	}
}
