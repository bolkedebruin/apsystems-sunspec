package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// newFreqDroopWriterFixture builds a writer pinned to the DS3 family
// (model 0x20). Tests that need a different family use
// newFreqDroopWriterFixtureModel instead.
func newFreqDroopWriterFixture(t *testing.T, uid uint8, prot source.ProtectionParams, invUIDs ...string) (*FreqDroopWriter, string) {
	return newFreqDroopWriterFixtureModel(t, uid, 0x20, prot, invUIDs...)
}

func newFreqDroopWriterFixtureModel(t *testing.T, uid uint8, model int, prot source.ProtectionParams, invUIDs ...string) (*FreqDroopWriter, string) {
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

func TestFreqDroopWriter_KOfHappyPath(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// KOf = 400 deci-percent → 40 %P/Hz (VDE-AR-N 4105 default, 5% droop).
	if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{400}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_Frequency_Watt_Slope_set")
	if !ok {
		t.Fatal("expected slope queue row")
	}
	if got != 40.0 {
		t.Errorf("slope=%v want 40.0 (%%P/Hz)", got)
	}
}

func TestFreqDroopWriter_KOfRangeRejection(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// Below 100 (10 %P/Hz) is non-compliant. Above 1500 (150 %P/Hz) too steep.
	for _, kOf := range []uint16{0, 50, 99, 1501, 5000} {
		if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{kOf}); err == nil {
			t.Errorf("kOf=%d: expected range error, got nil", kOf)
		}
	}
}

func TestFreqDroopWriter_KOfValidExtremes(t *testing.T) {
	fdw, _ := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// 100 (10 %P/Hz) — SMA installer minimum
	if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{100}); err != nil {
		t.Errorf("kOf=100 (10 %%P/Hz): %v", err)
	}
	// 1500 (150 %P/Hz) — IEEE 1547-2018 default territory
	if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{1500}); err != nil {
		t.Errorf("kOf=1500 (150 %%P/Hz): %v", err)
	}
	// 167 (16.7 %P/Hz) — EN 50549-1 maximum allowed droop
	if err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{167}); err != nil {
		t.Errorf("kOf=167 (16.7 %%P/Hz, EN50549 12%% droop): %v", err)
	}
}

func TestFreqDroopWriter_RspTmsHappyPath(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixture(t, 2, activeProt(), "INV-A")
	// RspTms = 5 seconds (uint32 hi/lo).
	if err := fdw.Apply(context.Background(), freqDroopBodyRspTmsHi, []uint16{0, 5}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Delay_Time_set")
	if !ok {
		t.Fatal("expected Over_frequency_Watt_Delay_Time_set row")
	}
	if got != 5.0 {
		t.Errorf("delay=%v want 5.0", got)
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

// QS1A (model 0x18) accepts CA via the per-inverter path. DbOf write
// must land in set_protection_parameters_inverter as
// Over_frequency_Watt_Start_set.
func TestFreqDroopWriter_QS1A_DbOfRoutesDirect(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixtureModel(t, 2, 0x18, activeProt(), "INV-A")
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

// QS1A + KOf maps to Over_Frequency_Watt_Slope_set (CF). Both per-
// inverter direct AND gridProfile broadcast paths have been confirmed
// not to land CF on QS1A (firmware-level reject), so the writer must
// surface RouteUnsupported and NOT queue a row.
func TestFreqDroopWriter_QS1A_KOfRoutesUnsupported(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixtureModel(t, 2, 0x18, activeProt(), "INV-A")
	err := fdw.Apply(context.Background(), freqDroopBodyKOf, []uint16{400})
	if err == nil {
		t.Fatal("expected unsupported-on-QS1A error for slope")
	}
	if _, ok := readQueuedParam(t, dir, "INV-A", "Over_Frequency_Watt_Slope_set"); ok {
		t.Error("slope row must not be queued on the direct path for QS1A")
	}
}

// QS1A + RspTms maps to Over_frequency_Watt_Delay_Time_set (CG). Same
// QS1A firmware-level reject as CF. RouteUnsupported, no queue row.
func TestFreqDroopWriter_QS1A_RspTmsRoutesUnsupported(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixtureModel(t, 2, 0x18, activeProt(), "INV-A")
	err := fdw.Apply(context.Background(), freqDroopBodyRspTmsHi, []uint16{0, 5})
	if err == nil {
		t.Fatal("expected unsupported-on-QS1A error for delay")
	}
	if _, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Delay_Time_set"); ok {
		t.Error("delay row must not be queued on the direct path for QS1A")
	}
}

// Unknown model code (e.g. brand-new family we haven't catalogued) →
// RouteUnsupported. Don't queue silently; don't crash.
func TestFreqDroopWriter_UnknownFamilyRejected(t *testing.T) {
	fdw, dir := newFreqDroopWriterFixtureModel(t, 2, 999, activeProt(), "INV-A")
	if err := fdw.Apply(context.Background(), freqDroopBodyDbOfHi, encodeDbOf(50)); err == nil {
		t.Error("expected error for unknown family")
	}
	if _, ok := readQueuedParam(t, dir, "INV-A", "Over_frequency_Watt_Start_set"); ok {
		t.Error("must not queue for unknown family")
	}
}

// routeFor table-driven sanity check — pin the routing decisions the
// rest of the writer relies on so a careless edit can't silently
// reroute them.
func TestRouteFor(t *testing.T) {
	cases := []struct {
		family InverterFamily
		param  string
		want   WriteRoute
	}{
		{FamilyDS3, "Over_frequency_Watt_recover_High_set", RouteDirect},
		{FamilyDS3, "Over_Frequency_Watt_Slope_set", RouteDirect},
		{FamilyQS1A, "Over_frequency_Watt_Start_set", RouteDirect},
		{FamilyQS1A, "Over_frequency_Watt_High_set", RouteDirect},
		{FamilyQS1A, "Delt_P_Over_HF", RouteDirect},
		{FamilyQS1A, "Over_frequency_Watt_recover_High_set", RouteUnsupported},
		{FamilyQS1A, "Over_Frequency_Watt_Slope_set", RouteUnsupported},
		{FamilyQS1A, "Over_frequency_Watt_Delay_Time_set", RouteUnsupported},
		{FamilyQS1A, "Under_Frequency_Watt_Low_set", RouteDirect},
		{FamilyQS1A, "Under_Frequency_Watt_High_set", RouteDirect},
		{FamilyQS1A, "Over_frequency_Watt_Low_set", RouteDirect},
		{FamilyDS3, "Over_frequency_Watt_Low_set", RouteDirect},
		{FamilyDS3, "Under_Frequency_Watt_Low_set", RouteDirect},
		{FamilyUnknown, "anything", RouteUnsupported},
	}
	for _, tc := range cases {
		got := routeFor(tc.family, tc.param)
		if got != tc.want {
			t.Errorf("routeFor(%d, %q) = %d, want %d", tc.family, tc.param, got, tc.want)
		}
	}
}

// freqWattCurvePoints documents which SunSpec freq-watt registers map
// to which APsystems long-form name. Pin the contents so the writers'
// dispatch paths stay consistent with the table.
func TestFreqWattCurvePoints_DbOfMapping(t *testing.T) {
	var found *FreqWattCurvePoint
	for i := range freqWattCurvePoints {
		cp := &freqWattCurvePoints[i]
		if cp.Model == 711 && cp.Body == freqDroopBodyDbOfHi && cp.BodyLo == freqDroopBodyDbOfLo {
			found = cp
			break
		}
	}
	if found == nil {
		t.Fatal("expected a Model 711 curve-point entry covering DbOf body[12..13]")
	}
	if found.Aps != "Over_frequency_Watt_Start_set" {
		t.Errorf("DbOf maps to %q, want Over_frequency_Watt_Start_set", found.Aps)
	}
	if found.Code != "CA" {
		t.Errorf("DbOf code = %q, want CA", found.Code)
	}
	// Decode 50 cHz → 50.5 Hz, matching the wire encoding the inline
	// DbOf branch uses.
	if got := found.Decode(0, 50); got < 50.49 || got > 50.51 {
		t.Errorf("Decode(0,50) = %v, want ~50.5", got)
	}
}

// Pin the Model 134 mappings: each (Hz body offset → APsystems param)
// must match what FreqWattCurveWriter dispatches.
func TestFreqWattCurvePoints_Model134Mapping(t *testing.T) {
	wantByCode := map[string]string{
		"DH": "Under_Frequency_Watt_Low_set",
		"DI": "Under_Frequency_Watt_High_set",
		"CB": "Over_frequency_Watt_Low_set",
		"CC": "Over_frequency_Watt_High_set",
	}
	got := map[string]string{}
	for _, cp := range freqWattCurvePoints {
		if cp.Model != 134 {
			continue
		}
		got[cp.Code] = cp.Aps
	}
	for code, want := range wantByCode {
		if got[code] != want {
			t.Errorf("Model 134 code %s maps to %q, want %q", code, got[code], want)
		}
	}
	// Decode 5020 → 50.20 Hz (Hz_SF = -2).
	for _, cp := range freqWattCurvePoints {
		if cp.Model != 134 {
			continue
		}
		if got := cp.Decode(5020, 0); got < 50.19 || got > 50.21 {
			t.Errorf("Model 134 %s Decode(5020,0) = %v, want ~50.20", cp.Code, got)
		}
	}
}

func TestFamilyForModel(t *testing.T) {
	cases := []struct {
		code int
		want InverterFamily
	}{
		{0x18, FamilyQS1A},
		{8, FamilyQS1A},
		{0x20, FamilyDS3},
		{0x36, FamilyDS3},
		{0x29, FamilyQT2},
		{0x32, FamilyQT2},
		{7, FamilyYC600},
		{0x17, FamilyYC600},
		{5, FamilyYC1000},
		{0, FamilyUnknown},
		{999, FamilyUnknown},
	}
	for _, tc := range cases {
		got := familyForModel(tc.code)
		if got != tc.want {
			t.Errorf("familyForModel(%d) = %d, want %d", tc.code, got, tc.want)
		}
	}
}
