package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bolke/ecu-sunspec/internal/source"
)

func newEnterServiceWriterFixture(t *testing.T, uid uint8, invUIDs ...string) (*EnterServiceWriter, string) {
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
	return &EnterServiceWriter{
		uid:    uid,
		snap:   source.Snapshot{Inverters: invs},
		writer: w,
	}, dir
}

func readQueuedParam(t *testing.T, dir, uid, name string) (float64, bool) {
	t.Helper()
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer db.Close()
	var v float64
	err := db.QueryRow(`SELECT parameter_value FROM set_protection_parameters_inverter WHERE id=? AND parameter_name=?`, uid, name).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

// 60 seconds → uint32 = 0x0000003c → high=0, low=60.
func encodeUint32(v uint32) []uint16 {
	return []uint16{uint16(v >> 16), uint16(v & 0xFFFF)}
}

func TestEnterServiceWriter_HappyPath(t *testing.T) {
	esw, dir := newEnterServiceWriterFixture(t, 2, "INV-A")
	regs := encodeUint32(120) // 120 s
	if err := esw.Apply(context.Background(), 7, regs); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	v, ok := readQueuedParam(t, dir, "INV-A", "grid_recovery_time")
	if !ok {
		t.Fatal("expected queued grid_recovery_time row, none found")
	}
	if v != 120 {
		t.Errorf("queued value=%v want 120", v)
	}
}

func TestEnterServiceWriter_RangeRejection(t *testing.T) {
	esw, _ := newEnterServiceWriterFixture(t, 2, "INV-A")
	cases := []uint32{0, 9, 611, 1000}
	for _, sec := range cases {
		err := esw.Apply(context.Background(), 7, encodeUint32(sec))
		if err == nil {
			t.Errorf("seconds=%d: expected range error, got nil", sec)
		}
	}
}

func TestEnterServiceWriter_RegulatoryFieldRejected(t *testing.T) {
	esw, _ := newEnterServiceWriterFixture(t, 2, "INV-A")

	// Try to write ESVHi (offset 1) — must be rejected as a regulatory field.
	err := esw.Apply(context.Background(), 1, []uint16{12000})
	if err == nil {
		t.Error("expected error for write to ESVHi (regulatory)")
	}

	// Try to write ESHzHi (offset 3..4, uint32) — must be rejected.
	err = esw.Apply(context.Background(), 3, encodeUint32(5050))
	if err == nil {
		t.Error("expected error for write to ESHzHi (regulatory)")
	}

	// Try to write ESDlyTms but only the high half (partial write) — reject.
	err = esw.Apply(context.Background(), 7, []uint16{0})
	if err == nil {
		t.Error("expected error for partial ESDlyTms write (only high word)")
	}
}

func TestEnterServiceWriter_AggregateRejected(t *testing.T) {
	esw, _ := newEnterServiceWriterFixture(t, 1, "INV-A", "INV-B")
	err := esw.Apply(context.Background(), 7, encodeUint32(120))
	if err == nil {
		t.Error("expected error for write to aggregate unit ID 1")
	}
}

func TestEnterServiceWriter_UnknownUnitRejected(t *testing.T) {
	esw, _ := newEnterServiceWriterFixture(t, 99, "INV-A")
	err := esw.Apply(context.Background(), 7, encodeUint32(120))
	if err == nil {
		t.Error("expected error for unmapped unit ID")
	}
}
