package source

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRecordDB builds a /home/record.db lookalike: the Data table client
// uploads to EMA, with APS18 production rows and JSON meter rows.
func writeRecordDB(t *testing.T, path string, rows [][3]string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE Data(item INTEGER, record VARCHAR(74400), resendflag VARCHAR(20), date_time VARCHAR(16))`); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if _, err := db.Exec(`INSERT INTO Data VALUES(?, ?, ?, ?)`, i+1, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
}

func ts(t time.Time) string { return t.Format("20060102150405") }

func TestParseRecordDB(t *testing.T) {
	now := time.Date(2026, 11, 7, 13, 36, 0, 0, time.Local)
	var rows [][3]string
	// Production rows every 5 min from 11:35 to 13:35, except a Service
	// Mode gap from 12:27 to 13:01; the newest one is still pending ("2").
	newest := now.Truncate(5 * time.Minute) // 13:35
	gapFrom := time.Date(2026, 11, 7, 12, 27, 0, 0, time.Local)
	gapTo := time.Date(2026, 11, 7, 13, 1, 0, 0, time.Local)
	for k := 24; k >= 0; k-- {
		at := newest.Add(-time.Duration(k) * 5 * time.Minute)
		if at.After(gapFrom) && at.Before(gapTo) {
			continue
		}
		flag := "0"
		if k == 0 {
			flag = "2"
		}
		rows = append(rows, [3]string{"APS18AA293AAAAAAA1215000054677" + ts(at) + "END", flag, ts(at)})
		rows = append(rows, [3]string{`{"VER":"M0001","A":"215000054677"}`, "0", ts(at.Add(4 * time.Minute))})
	}
	path := filepath.Join(t.TempDir(), "record.db")
	writeRecordDB(t, path, rows)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	st, err := ParseRecordDB(context.Background(), raw, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := time.Date(2026, 11, 7, 13, 35, 0, 0, time.Local); !st.LastProdRecord.Equal(want) {
		t.Errorf("LastProdRecord=%v want %v", st.LastProdRecord, want)
	}
	if st.ProdRecordsPending != 1 {
		t.Errorf("ProdRecordsPending=%d want 1", st.ProdRecordsPending)
	}
	// Last hour = 12:36..13:36: only 13:05..13:35 survive the gap → 7 rows.
	if st.ProdRecordsLastHour != 7 {
		t.Errorf("ProdRecordsLastHour=%d want 7", st.ProdRecordsLastHour)
	}
}

func TestParseRecordDB_Garbage(t *testing.T) {
	if _, err := ParseRecordDB(context.Background(), []byte("not a database"), time.Now()); err == nil {
		t.Error("expected an error for a torn / non-SQLite buffer")
	}
}

// Tail of /home/control_client.log: control_client contacts EMA every 15 min.
func TestLastEMAControlContact(t *testing.T) {
	log := strings.Join([]string{
		"(2026-11-07 13:12:34)Connect to EMA successfully SSL",
		"(2026-11-07 13:12:34)>>End",
		"(2026-11-07 13:27:35)>>Start Communication with EMA",
		"(2026-11-07 13:27:35)Connect to EMA successfully SSL",
		"(2026-11-07 13:27:35)Close socket",
		"(2026-11-07 13:42:36)>>Start Communication with EMA",
		"(2026-11-07 13:42:36)Failed to connect EMA",
	}, "\n")
	got := LastEMAControlContact(log)
	if want := time.Date(2026, 11, 7, 13, 27, 35, 0, time.Local); !got.Equal(want) {
		t.Errorf("LastEMAControlContact=%v want %v", got, want)
	}
	if got := LastEMAControlContact("no contact lines here\n"); !got.IsZero() {
		t.Errorf("got %v want zero", got)
	}
}

func TestReadEMAConnected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connectemaflag.txt")
	if got := ReadEMAConnected(path); got != EMAUnknown {
		t.Errorf("missing file: %v want EMAUnknown", got)
	}
	os.WriteFile(path, []byte("connected\n"), 0o644)
	if got := ReadEMAConnected(path); got != EMAConnected {
		t.Errorf("connected: %v want EMAConnected", got)
	}
	os.WriteFile(path, []byte("disconnected\n"), 0o644)
	if got := ReadEMAConnected(path); got != EMADisconnected {
		t.Errorf("disconnected: %v want EMADisconnected", got)
	}
}

// Build reads record.db and control_client.log at most once per
// EMAInterval: they live on the ECU's flash and belong to APsystems'
// processes, so the 5 s Modbus refresh must not hit them every time.
func TestBuild_EMAStatusThrottled(t *testing.T) {
	dir := t.TempDir()
	params := filepath.Join(dir, "parameters_app.conf")
	os.WriteFile(params, []byte("01,1,20261107133500\n901500081373,0,02\n"), 0o644)
	rec := filepath.Join(dir, "record.db")
	now := time.Date(2026, 11, 7, 13, 36, 0, 0, time.Local)
	writeRecordDB(t, rec, [][3]string{{"APS18…", "2", ts(now.Add(-time.Minute).Truncate(5 * time.Minute))}})
	ctl := filepath.Join(dir, "control_client.log")
	os.WriteFile(ctl, []byte("(2026-11-07 13:27:35)Connect to EMA successfully SSL\n"), 0o644)
	flag := filepath.Join(dir, "connectemaflag.txt")
	os.WriteFile(flag, []byte("connected"), 0o644)

	clock := now
	b := &Builder{
		ParamsFile:  params,
		RecordDB:    rec,
		ControlLog:  ctl,
		EMAFlagFile: flag,
		EMAInterval: time.Minute,
		Now:         func() time.Time { return clock },
	}
	s, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.EMA.Connected != EMAConnected || s.EMA.ProdRecordsPending != 1 || s.EMA.LastControlContact.IsZero() {
		t.Fatalf("first build EMA=%+v", s.EMA)
	}

	// record.db changes, but the next refresh is within EMAInterval:
	// the cached status is kept.
	os.Remove(rec)
	writeRecordDB(t, rec, nil)
	clock = now.Add(5 * time.Second)
	s, _ = b.Build(context.Background())
	if s.EMA.ProdRecordsPending != 1 {
		t.Errorf("re-read record.db within EMAInterval: pending=%d want cached 1", s.EMA.ProdRecordsPending)
	}

	clock = now.Add(61 * time.Second)
	s, _ = b.Build(context.Background())
	if s.EMA.ProdRecordsPending != 0 {
		t.Errorf("after EMAInterval: pending=%d want 0 (re-read)", s.EMA.ProdRecordsPending)
	}
}
