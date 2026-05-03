package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// Reverter holds reversion timers that auto-restore full power after
// SunSpec Model 123 WMaxLimPct_RvrtTms expires without a refresh from the
// controller.
//
// The Model 123 spec lets the writer specify a reversion timeout — if no
// further writes refresh the cap, the inverter is supposed to revert to
// no-limit (pre-2018 layout, used here). APsystems firmware doesn't
// honor that natively, so we keep the timer here and call
// Writer.RestoreFullPower for each affected inverter when it fires.
//
// Victron's dbus-fronius writes RvrtTms=120s and refreshes every 60s, so
// in normal operation the timer never fires. It does fire if the
// controller crashes or the LAN drops — restoring inverters to full
// output is the safe-default behavior matching pre-2018 Model 123.
type Reverter struct {
	mu     sync.Mutex
	timers map[uint8]*time.Timer
	writer *source.Writer
	logger *log.Logger
}

// NewReverter builds a Reverter bound to the given Writer. Returns nil if
// w is nil — the server checks for nil before using.
func NewReverter(w *source.Writer, lg *log.Logger) *Reverter {
	if w == nil {
		return nil
	}
	if lg == nil {
		lg = log.Default()
	}
	return &Reverter{
		timers: map[uint8]*time.Timer{},
		writer: w,
		logger: lg,
	}
}

// Schedule starts (or resets) a reversion timer keyed by Modbus unit ID.
// When the timer fires, RestoreFullPower is called for each target UID.
//
// after must be > 0 to schedule; 0 (or any non-positive duration) cancels
// any existing timer for this uid without scheduling a new one.
func (r *Reverter) Schedule(uid uint8, after time.Duration, targets []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.timers[uid]; ok {
		t.Stop()
		delete(r.timers, uid)
	}
	if after <= 0 || len(targets) == 0 {
		return
	}
	targetsCopy := append([]string(nil), targets...)
	r.timers[uid] = time.AfterFunc(after, func() {
		r.fire(uid, targetsCopy)
	})
}

// Cancel stops any pending reversion timer for uid (no-op if none).
func (r *Reverter) Cancel(uid uint8) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.timers[uid]; ok {
		t.Stop()
		delete(r.timers, uid)
	}
}

// Stop cancels every pending timer (called on server shutdown).
func (r *Reverter) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for uid, t := range r.timers {
		t.Stop()
		delete(r.timers, uid)
	}
}

// pending reports whether a timer is currently scheduled for uid (test hook).
func (r *Reverter) pending(uid uint8) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.timers[uid]
	return ok
}

func (r *Reverter) fire(uid uint8, targets []string) {
	r.mu.Lock()
	delete(r.timers, uid)
	r.mu.Unlock()
	ctx := context.Background()
	for _, t := range targets {
		if err := r.writer.RestoreFullPower(ctx, t); err != nil {
			r.logger.Printf("reverter: uid=%d restore %s: %v", uid, t, err)
			continue
		}
		r.logger.Printf("reverter: uid=%d restored %s to full power (RvrtTms expired)", uid, t)
	}
}
