package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
)

// newFreqWattCurveWriterFixture mirrors the freq-droop fixture but for
// Model 134. Defaults to DS3 (model 0x20).
func newFreqWattCurveWriterFixture(t *testing.T, uid uint8, model int, prot source.ProtectionParams, invUIDs ...string) (*FreqWattCurveWriter, string) {
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
		invs = append(invs, source.Inverter{UID: u, Online: true, TypeCode: "01", Model: model})
	}
	protMap := map[string]source.ProtectionParams{}
	for _, u := range invUIDs {
		protMap[u] = prot
	}
	return &FreqWattCurveWriter{
		uid:    uid,
		snap:   source.Snapshot{Inverters: invs, Protection: protMap},
		writer: w,
	}, dir
}

func curveProt() source.ProtectionParams {
	return source.ProtectionParams{
		OFFast:        52.0, // AK
		OFCurveUFLow:  47.0, // DH
		OFCurveUFHigh: 49.5, // DI
		OFCurveOFLow:  50.5, // CB
		OFDroopEnd:    52.0, // CC
		OFDroopMode:   13,
		Has: map[string]bool{
			"AK": true, "DH": true, "DI": true, "CB": true, "CC": true, "CV": true,
		},
	}
}

// DS3 + Hz3 (CB) write → RouteDirect, value lands in queue at 50.5 Hz.
func TestFreqWattCurveWriter_DS3_Hz3_Direct(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x20, curveProt(), "INV-A")
	// Hz3 wire = 51.20 Hz × 100 = 5120
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz3Off, []uint16{5120}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Low_set")
	if !ok {
		t.Fatal("expected Over_frequency_Watt_Low_set queued row")
	}
	if got < 51.19 || got > 51.21 {
		t.Errorf("CB=%v want ~51.20", got)
	}
}

// QS1A + Hz3 (CB) write → RouteDirect (per qs1a-probe-results.md, 2026-05-04).
func TestFreqWattCurveWriter_QS1A_Hz3_Direct(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x18, curveProt(), "INV-A")
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz3Off, []uint16{5050}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Low_set")
	if !ok {
		t.Fatal("expected Over_frequency_Watt_Low_set queued row on QS1A")
	}
	if got < 50.49 || got > 50.51 {
		t.Errorf("CB=%v want ~50.50", got)
	}
}

// QS1A + Hz1 (DH) — under-frequency. Dispatcher confirms it routes
// direct on QS1A; live-untested but accepted via routeFor.
func TestFreqWattCurveWriter_QS1A_Hz1_Direct(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x18, curveProt(), "INV-A")
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz1Off, []uint16{4700}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Under_Frequency_Watt_Low_set")
	if !ok {
		t.Fatal("expected Under_Frequency_Watt_Low_set queued row")
	}
	if got < 46.99 || got > 47.01 {
		t.Errorf("DH=%v want ~47.00", got)
	}
}

// Round trip: Hz3 wire 5120 → queue value 51.20.
func TestFreqWattCurveWriter_Hz3_RoundTrip(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x20, curveProt(), "INV-A")
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz3Off, []uint16{5120}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Low_set")
	if got != 51.20 {
		t.Errorf("round-trip Hz3 wire 5120 → %v want 51.20", got)
	}
}

// Writing W (W%) is rejected — the curve roles fix W at 0/100/100/0.
func TestFreqWattCurveWriter_RejectsWWrite(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x20, curveProt(), "INV-A")
	// W3 wire = 80% — non-spec value, must error.
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyW3Off, []uint16{80}); err == nil {
		t.Error("expected rejection on W3 write")
	}
	if _, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Low_set"); ok {
		t.Error("must not queue when W% write is rejected")
	}
}

// Header / scale-factor writes are rejected.
func TestFreqWattCurveWriter_RejectsHeaderWrites(t *testing.T) {
	fwc, _ := newFreqWattCurveWriterFixture(t, 2, 0x20, curveProt(), "INV-A")
	if err := fwc.Apply(context.Background(), 0 /* ActCrv */, []uint16{2}); err == nil {
		t.Error("expected error on ActCrv write")
	}
}

// Aggregate uid 1 is not a valid target for Model 134.
func TestFreqWattCurveWriter_AggregateRejected(t *testing.T) {
	fwc, _ := newFreqWattCurveWriterFixture(t, 1, 0x20, curveProt(), "INV-A", "INV-B")
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz3Off, []uint16{5120}); err == nil {
		t.Error("expected error for aggregate uid 1")
	}
}

// Unknown family → RouteUnsupported, no queue row.
func TestFreqWattCurveWriter_UnknownFamilyRejected(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 999, curveProt(), "INV-A")
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz3Off, []uint16{5120}); err == nil {
		t.Error("expected error for unknown family")
	}
	if _, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Low_set"); ok {
		t.Error("must not queue for unknown family")
	}
}

// Multi-point write: writing Hz1+Hz2+Hz3+Hz4 in one FC16 call should
// queue all four. The address-offset pins to Hz1 and the regs span
// through Hz4, but the write covers W1..W3 in between which would be
// read as 0 — those are rejected as W% writes.
//
// Practical clients write one Hz at a time; we still pin the
// "single-Hz" path here so callers don't accidentally cover W%.
func TestFreqWattCurveWriter_SingleHzPerWrite(t *testing.T) {
	fwc, dir := newFreqWattCurveWriterFixture(t, 2, 0x20, curveProt(), "INV-A")
	// Hz4 alone (write at body[17] of length 1)
	if err := fwc.Apply(context.Background(), sunspec.FreqWattCurveBodyHz4Off, []uint16{5200}); err != nil {
		t.Fatalf("Hz4 write: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_High_set")
	if !ok || got != 52.0 {
		t.Errorf("Hz4 round-trip: ok=%v got=%v want 52.0", ok, got)
	}
}
