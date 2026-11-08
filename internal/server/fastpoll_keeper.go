package server

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// Service Mode (fast polling) modes, as written to Model 64203 FastPoll.
const (
	FastPollOff  = 0 // normal 5-minute polling; the flag is left alone
	FastPollOn   = 1 // Service Mode, kept on until set back to off
	FastPollAuto = 2 // Service Mode while production is at or above the threshold
)

// DefaultFastPollThresholdW is the auto-mode production threshold.
const DefaultFastPollThresholdW = 50

const (
	// fastPollReassertEvery bounds how often the flag (on flash) is
	// rewritten after main.exe resets it.
	fastPollReassertEvery = time.Minute
	// fastPollAutoDwell is the minimum time between auto-mode switches:
	// each switch back to normal mode costs at least one EMA record.
	fastPollAutoDwell = 10 * time.Minute
	// fastPollAutoBelowFor is how long production must stay below the low
	// threshold before auto mode leaves Service Mode, so a single bad
	// reading (radio miss, file read mid-rewrite) doesn't switch it off.
	fastPollAutoBelowFor = 2 * time.Minute
)

// FastPollKeeper holds the Service Mode choice made over Modbus and
// applies it to the ECU flag (fast_polling.conf). main.exe drops Service
// Mode by itself after 60 minutes; while the choice calls for it, Tick
// writes "1" back.
//
//   - off: the keeper never touches the flag, so the EMA app or remote
//     control may still use Service Mode.
//   - on: Service Mode all the time.
//   - auto: Service Mode while system production is at or above the
//     threshold, normal mode once it has stayed below threshold −
//     max(10 W, 20 %) for fastPollAutoBelowFor, at most one switch per
//     fastPollAutoDwell. Dawn and dusk run in normal mode, so main.exe
//     queues its records for EMA then.
//
// Mode and threshold are saved in stateFile ("<mode> <threshold>", on
// flash, written only when they change) and survive restarts.
type FastPollKeeper struct {
	flagFile  string
	stateFile string
	logger    *log.Logger

	mu         sync.Mutex
	mode       int
	thresholdW int
	lastAssert time.Time // last re-assert after an ECU reset
	lastSwitch time.Time // last auto-mode switch
	belowSince time.Time // auto mode: production below the low threshold since
	written    bool      // last value the keeper wrote to the flag
}

// NewFastPollKeeper loads the saved mode and threshold from stateFile.
func NewFastPollKeeper(flagFile, stateFile string, logger *log.Logger) *FastPollKeeper {
	k := &FastPollKeeper{flagFile: flagFile, stateFile: stateFile, logger: logger,
		thresholdW: DefaultFastPollThresholdW}
	if stateFile == "" {
		return k
	}
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return k
	}
	f := strings.Fields(string(b))
	if len(f) >= 1 {
		if m, err := strconv.Atoi(f[0]); err == nil && m >= FastPollOff && m <= FastPollAuto {
			k.mode = m
		}
	}
	if len(f) >= 2 {
		if w, err := strconv.Atoi(f[1]); err == nil && w > 0 {
			k.thresholdW = w
		}
	}
	k.written, _ = source.ReadFastPolling(flagFile)
	return k
}

// Mode returns the saved mode.
func (k *FastPollKeeper) Mode() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.mode
}

// ThresholdW returns the auto-mode threshold.
func (k *FastPollKeeper) ThresholdW() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.thresholdW
}

// SetMode records the mode. on and off apply to the flag right away;
// auto waits for the next production reading.
func (k *FastPollKeeper) SetMode(mode int) error {
	if mode < FastPollOff || mode > FastPollAuto {
		return fmt.Errorf("fast polling mode %d: want 0 (off), 1 (on) or 2 (auto)", mode)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.save(mode, k.thresholdW); err != nil {
		return err
	}
	k.mode = mode
	k.lastAssert, k.lastSwitch = time.Time{}, time.Time{}
	switch mode {
	case FastPollOn:
		return k.write(true)
	case FastPollOff:
		return k.write(false)
	}
	return nil
}

// SetThresholdW records the auto-mode threshold.
func (k *FastPollKeeper) SetThresholdW(w int) error {
	if w <= 0 || w > 65534 {
		return fmt.Errorf("fast polling threshold %d W: want 1..65534", w)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.save(k.mode, w); err != nil {
		return err
	}
	k.thresholdW = w
	return nil
}

// Tick applies the mode given the current system production.
func (k *FastPollKeeper) Tick(now time.Time, productionW int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	on, _ := source.ReadFastPolling(k.flagFile)
	switch k.mode {
	case FastPollOn:
		if !on {
			k.reassert(now)
		}
	case FastPollAuto:
		if !on && k.written {
			// main.exe's 60-minute exit while the keeper wants it on.
			k.reassert(now)
			return
		}
		hyst := k.thresholdW / 5
		if hyst < 10 {
			hyst = 10
		}
		if productionW >= k.thresholdW-hyst {
			k.belowSince = time.Time{}
		} else if k.belowSince.IsZero() {
			k.belowSince = now
		}
		want := on
		if !on && productionW >= k.thresholdW {
			want = true
		} else if on && !k.belowSince.IsZero() && now.Sub(k.belowSince) >= fastPollAutoBelowFor {
			want = false
		}
		if want == on || (!k.lastSwitch.IsZero() && now.Sub(k.lastSwitch) < fastPollAutoDwell) {
			return
		}
		if err := k.write(want); err != nil {
			k.logger.Printf("fast polling: auto switch to %v: %v", want, err)
			return
		}
		k.lastSwitch = now
		k.logger.Printf("fast polling: auto, %d W (threshold %d W): Service Mode %v", productionW, k.thresholdW, want)
	}
}

func (k *FastPollKeeper) reassert(now time.Time) {
	if !k.lastAssert.IsZero() && now.Sub(k.lastAssert) < fastPollReassertEvery {
		return
	}
	k.lastAssert = now
	if err := k.write(true); err != nil {
		k.logger.Printf("fast polling: re-enable Service Mode: %v", err)
		return
	}
	k.logger.Printf("fast polling: ECU left Service Mode, re-enabled")
}

func (k *FastPollKeeper) write(on bool) error {
	if err := source.SetFastPolling(k.flagFile, on); err != nil {
		return err
	}
	k.written = on
	return nil
}

// save writes the state file when mode or threshold change.
func (k *FastPollKeeper) save(mode, thresholdW int) error {
	if k.stateFile == "" || (mode == k.mode && thresholdW == k.thresholdW && k.stateExists()) {
		return nil
	}
	return os.WriteFile(k.stateFile, []byte(fmt.Sprintf("%d %d\n", mode, thresholdW)), 0o644)
}

func (k *FastPollKeeper) stateExists() bool {
	_, err := os.Stat(k.stateFile)
	return err == nil
}
