package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bolke/ecu-sunspec/internal/source"
)

func newFreqDroopWriterFixture(t *testing.T, uid uint8, prot source.ProtectionParams, invUIDs ...string) (*FreqDroopWriter, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE set_protection_parameters_inverter(id VARCHAR(256), parameter_name VARCHAR(256), parameter_value REAL, set_flag INTEGER, primary key(id, parameter_name))`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	w, err := source.OpenWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	invs := make([]source.Inverter, 0, len(invUIDs))
	for _, u := range invUIDs {
		invs = append(invs, source.Inverter{UID: u, Online: true, TypeCode: "01"})
	}
	protMap := map[string]source.ProtectionParams{}
	for _, u := range invUIDs {
		protMap[u] = prot
	}
	return &FreqDroopWriter{
		uid:    uid,
		snap:   source.Snapshot{Inverters: invs, Protection: protMap},
		writer: w,
	}, dir
}

// activeProt returns a typical EN 50549-1 NL profile: OF1=52.0, droop
// starts at 50.2 Hz. Has flags set so cross-param checks have data.
func activeProt() source.ProtectionParams {
	return source.ProtectionParams{
		OFFast:       52.0, // AK
		OFDroopStart: 50.2, // DC
		OFDroopEnd:   52.0, // CC
		OFDroopSlope: 16.7, // DD (units uncertain — see freq_droop_writer.go)
		OFDroopMode:  13,   // CV (AS/NZS variant)
		Has: map[string]bool{
			"AK": true, "DC": true, "CC": true, "DD": true, "CV": true,
		},
	}
}

func encodeDbOf(dbOf uint32) []uint16 {
	return []uint16{uint16(dbOf >> 16), uint16(dbOf & 0xFFFF)}
}

func TestFreqDroopWriter_DbOfHappyPath(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// DbOf = 50 cHz → HzStart = 50.5 Hz. OF1=52.0, margin=0.5 → 50.5 ≤ 51.5 OK.
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(50)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Start_set")
	if !ok {
		t.Fatal("expected Over_frequency_Watt_Start_set row")
	}
	if got < 50.49 || got > 50.51 {
		t.Errorf("HzStart=%v want ~50.5", got)
	}
}

func TestFreqDroopWriter_DbOfTooCloseToTrip(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// DbOf = 160 cHz → HzStart = 51.6 Hz. OF1=52.0, margin=0.5 → max=51.5.
	// 51.6 > 51.5 → reject.
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(160)); err == nil {
		t.Error("expected rejection (HzStart too close to OF1 trip)")
	}
}

func TestFreqDroopWriter_DbOfBelowNominal(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// DbOf = 0 → HzStart = 50.0 Hz, must be > 50.0.
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(0)); err == nil {
		t.Error("expected rejection (HzStart=50.0)")
	}
}

func TestFreqDroopWriter_AggregateRejected(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 1, activeProt(), "INV-A", "INV-B")
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(50)); err == nil {
		t.Error("expected error for aggregate uid 1")
	}
}

func TestFreqDroopWriter_RequiresAKToValidate(t *testing.T) {
	prot := activeProt()
	prot.Has["AK"] = false
	fdw, _ := newFreqDroopWriterFixture(t, 2, prot, "INV-A")
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(50)); err == nil {
		t.Error("expected error when AK unavailable for cross-check")
	}
}

func TestFreqDroopWriter_KOfReadOnly(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// KOf at offset 16 must be rejected this iteration.
	if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{555}); err == nil {
		t.Error("expected error for KOf write (read-only this iteration)")
	}
}

func TestFreqDroopWriter_ReadOnlyFieldsRejected(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	cases := []struct {
		name string
		off  uint16
		val  []uint16
	}{
		{"AdptCtlRslt", 2, []uint16{1}},
		{"NCtl", 3, []uint16{2}},
		{"Db_SF", 9, []uint16{0xFFFE}},
		{"DbUf", 14, encodeDbOf(50)},
		{"PMin", 20, []uint16{50}},
		{"ReadOnly", 21, []uint16{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := fdw.Apply(context.Background(), tc.off, tc.val); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestFreqDroopWriter_EnaWrite(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// Disable
	if err := fdw.Apply(context.Background(), 0, []uint16{0}); err != nil {
		t.Fatalf("Ena=0 write: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_set")
	if !ok {
		t.Fatal("expected Over_frequency_Watt_set row")
	}
	if got != float64(freqDroopModeDisabled) {
		t.Errorf("disabled mode=%v want %d", got, freqDroopModeDisabled)
	}
}
