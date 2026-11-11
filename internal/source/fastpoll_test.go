package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFastPolling_MissingFileIsOff(t *testing.T) {
	on, _ := ReadFastPolling(filepath.Join(t.TempDir(), "fast_polling.conf"))
	if on {
		t.Error("missing fast_polling.conf should read as off")
	}
}

func TestFastPolling_SetAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fast_polling.conf")

	before := time.Now().Add(-time.Second)
	if err := SetFastPolling(path, true); err != nil {
		t.Fatalf("set on: %v", err)
	}
	on, since := ReadFastPolling(path)
	if !on {
		t.Error("expected on after SetFastPolling(true)")
	}
	if since.Before(before) {
		t.Errorf("since=%v, want >= %v (file mtime)", since, before)
	}
	// Same format as the ECU web UI reads with intval(fgets()).
	if b, _ := os.ReadFile(path); string(b) != "1\n" {
		t.Errorf("file=%q want %q", b, "1\n")
	}

	if err := SetFastPolling(path, false); err != nil {
		t.Fatalf("set off: %v", err)
	}
	if on, _ := ReadFastPolling(path); on {
		t.Error("expected off after SetFastPolling(false)")
	}
}

// Build exposes the ECU's last radio cycle (parameters_app.conf header) and
// the Service Mode state from <YunengDir>/fast_polling.conf.
func TestBuild_LastPollAndFastPolling(t *testing.T) {
	dir := t.TempDir()
	params := filepath.Join(dir, "parameters_app.conf")
	if err := os.WriteFile(params, []byte("01,1,20261107122500\n901500081373,0,02\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// In Service Mode main.exe rewrites the file every ~21 s but the header
	// timestamp is truncated to the minute; the file mtime is the precise
	// time of the last radio cycle.
	want := time.Date(2026, 11, 7, 12, 25, 21, 0, time.Local)
	if err := os.Chtimes(params, want, want); err != nil {
		t.Fatal(err)
	}
	yuneng := t.TempDir()
	if err := SetFastPolling(filepath.Join(yuneng, "fast_polling.conf"), true); err != nil {
		t.Fatal(err)
	}

	b := &Builder{ParamsFile: params, YunengDir: yuneng}
	s, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !s.LastPollTime.Equal(want) {
		t.Errorf("LastPollTime=%v want %v", s.LastPollTime, want)
	}
	if !s.FastPolling {
		t.Error("FastPolling=false want true")
	}
	if s.FastPollingSince.IsZero() {
		t.Error("FastPollingSince is zero, want the file mtime")
	}
}
