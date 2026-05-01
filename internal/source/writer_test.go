package source

import (
	"context"
	"database/sql"
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
