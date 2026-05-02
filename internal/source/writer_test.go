package source

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// makeWriterFixture creates a fresh database.db with the schema bits Writer
// actually touches: power and turn_on_off. Avoids depending on the full
// production schema in testdata/, since that's read-only.
func makeWriterFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "database.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec(t, db, `CREATE TABLE power(item INTEGER, id VARCHAR(15), limitedpower INTEGER, limitedresult INTEGER, stationarypower INTEGER, stationaryresult INTEGER, flag INTEGER)`)
	mustExec(t, db, `CREATE TABLE turn_on_off(id VARCHAR(256), set_flag INTEGER, primary key(id))`)
	mustExec(t, db, `CREATE TABLE set_protection_parameters_inverter(id VARCHAR(256), parameter_name VARCHAR(256), parameter_value REAL, set_flag INTEGER, primary key(id, parameter_name))`)
	mustExec(t, db, `INSERT INTO power VALUES(0,'INV-A',258,0,258,'-',0)`)
	mustExec(t, db, `INSERT INTO power VALUES(1,'INV-B',258,0,258,'-',0)`)
	return dir
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("setup %q: %v", q, err)
	}
}

func TestWriter_SetMaxPower(t *testing.T) {
	dir := makeWriterFixture(t)
	w, err := OpenWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.SetMaxPower(context.Background(), "INV-A", 200); err != nil {
		t.Fatalf("SetMaxPower: %v", err)
	}

	// Verify via a fresh read.
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer db.Close()
	var lp, flag int
	if err := db.QueryRow(`SELECT limitedpower, flag FROM power WHERE id='INV-A'`).
		Scan(&lp, &flag); err != nil {
		t.Fatal(err)
	}
	if lp != 200 {
		t.Errorf("limitedpower=%d want 200", lp)
	}
	if flag != 1 {
		t.Errorf("flag=%d want 1 (so main.exe dispatches it)", flag)
	}
}

func TestWriter_SetMaxPower_RangeCheck(t *testing.T) {
	dir := makeWriterFixture(t)
	w, _ := OpenWriter(dir)
	defer w.Close()
	cases := []int{0, 19, 501, 1000}
	for _, p := range cases {
		if err := w.SetMaxPower(context.Background(), "INV-A", p); err == nil {
			t.Errorf("p=%d: expected range error, got nil", p)
		}
	}
}

func TestWriter_SetTurnOnOff(t *testing.T) {
	dir := makeWriterFixture(t)
	w, _ := OpenWriter(dir)
	defer w.Close()

	for _, on := range []bool{true, false, true} {
		if err := w.SetTurnOnOff(context.Background(), "INV-A", on); err != nil {
			t.Fatal(err)
		}
	}

	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer db.Close()
	var state int
	db.QueryRow(`SELECT set_flag FROM turn_on_off WHERE id='INV-A'`).Scan(&state)
	if state != 1 {
		t.Errorf("expected last write (on) to leave state=1, got %d", state)
	}
}

func TestWriter_SetProtectionParam(t *testing.T) {
	dir := makeWriterFixture(t)
	w, _ := OpenWriter(dir)
	defer w.Close()

	// Two writes for same (uid, name) — second must replace the first.
	if err := w.SetProtectionParam(context.Background(), "INV-A", "grid_recovery_time", 30.0); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := w.SetProtectionParam(context.Background(), "INV-A", "grid_recovery_time", 60.0); err != nil {
		t.Fatalf("second write: %v", err)
	}
	// Different (uid, name) creates a separate row.
	if err := w.SetProtectionParam(context.Background(), "INV-B", "grid_recovery_time", 60.0); err != nil {
		t.Fatalf("INV-B write: %v", err)
	}

	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer db.Close()
	rows, err := db.Query(`SELECT id, parameter_name, parameter_value, set_flag FROM set_protection_parameters_inverter ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var id, name string
		var v float64
		var flag int
		if err := rows.Scan(&id, &name, &v, &flag); err != nil {
			t.Fatal(err)
		}
		if flag != 1 {
			t.Errorf("set_flag=%d want 1 (queued)", flag)
		}
		seen = append(seen, id+":"+name+":"+fmt.Sprintf("%.0f", v))
	}
	want := []string{"INV-A:grid_recovery_time:60", "INV-B:grid_recovery_time:60"}
	if len(seen) != len(want) {
		t.Errorf("got %v rows, want %v", seen, want)
	}
	for i, w := range want {
		if i >= len(seen) || seen[i] != w {
			t.Errorf("row %d = %q, want %q", i, seen[i], w)
		}
	}
}

func TestWriter_SetProtectionParam_RejectsEmpty(t *testing.T) {
	dir := makeWriterFixture(t)
	w, _ := OpenWriter(dir)
	defer w.Close()
	ctx := context.Background()

	if err := w.SetProtectionParam(ctx, "", "grid_recovery_time", 60.0); err == nil {
		t.Error("empty UID: expected error, got nil")
	}
	if err := w.SetProtectionParam(ctx, "INV-A", "", 60.0); err == nil {
		t.Error("empty name: expected error, got nil")
	}
}

func TestWriter_RestoreFullPower(t *testing.T) {
	dir := makeWriterFixture(t)
	w, _ := OpenWriter(dir)
	defer w.Close()
	if err := w.RestoreFullPower(context.Background(), "INV-A"); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dir, "database.db")+"?mode=ro")
	defer db.Close()
	var lp int
	db.QueryRow(`SELECT limitedpower FROM power WHERE id='INV-A'`).Scan(&lp)
	if lp != MaxPanelLimitW {
		t.Errorf("limitedpower=%d want %d", lp, MaxPanelLimitW)
	}
	_ = os.RemoveAll(dir)
}
