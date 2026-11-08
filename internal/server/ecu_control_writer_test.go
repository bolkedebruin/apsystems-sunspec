package server

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"testing"

	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
)

func newECUControlWriter(t *testing.T, uid uint8) (*ECUControlWriter, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fast_polling.conf")
	k := NewFastPollKeeper(path, filepath.Join(dir, "sunspec-fastpoll.conf"), log.New(io.Discard, "", 0))
	return &ECUControlWriter{uid: uid, keeper: k}, path
}

func TestECUControlWriter_TogglesServiceMode(t *testing.T) {
	w, path := newECUControlWriter(t, 1)
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{1}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if on, _ := source.ReadFastPolling(path); !on {
		t.Error("Service Mode not enabled")
	}
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{0}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if on, _ := source.ReadFastPolling(path); on {
		t.Error("Service Mode not disabled")
	}
}

func TestECUControlWriter_AutoMode(t *testing.T) {
	w, _ := newECUControlWriter(t, 1)
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{2}); err != nil {
		t.Fatalf("FastPoll=2: %v", err)
	}
	if got := w.keeper.Mode(); got != FastPollAuto {
		t.Errorf("mode=%d want auto", got)
	}
}

func TestECUControlWriter_RejectsBadValue(t *testing.T) {
	w, _ := newECUControlWriter(t, 1)
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{3}); err == nil {
		t.Error("FastPoll=3 accepted; want error")
	}
}

func TestECUControlWriter_Threshold(t *testing.T) {
	w, _ := newECUControlWriter(t, 1)
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyThresholdOff, []uint16{150}); err != nil {
		t.Fatalf("threshold: %v", err)
	}
	if got := w.keeper.ThresholdW(); got != 150 {
		t.Errorf("threshold=%d want 150", got)
	}
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyThresholdOff, []uint16{0}); err == nil {
		t.Error("threshold 0 accepted")
	}
}

func TestECUControlWriter_RejectsReadOnlyFields(t *testing.T) {
	w, _ := newECUControlWriter(t, 1)
	// FastPollElapsedS sits right after FastPoll.
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff+1, []uint16{60}); err == nil {
		t.Error("write to FastPollElapsedS accepted; want error")
	}
}

func TestECUControlWriter_AggregateOnly(t *testing.T) {
	w, _ := newECUControlWriter(t, 2)
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{1}); err == nil {
		t.Error("write on unit 2 accepted; want error (ECU-wide setting, unit 1 only)")
	}
}

func TestECUControlWriter_NoFileConfigured(t *testing.T) {
	w := &ECUControlWriter{uid: 1}
	if err := w.Apply(context.Background(), sunspec.ECUControlBodyFastPollOff, []uint16{1}); err == nil {
		t.Error("write accepted without a Service Mode keeper; want error")
	}
}
