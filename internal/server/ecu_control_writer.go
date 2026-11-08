package server

import (
	"context"
	"fmt"

	"github.com/bolke/ecu-sunspec/internal/sunspec"
)

// ECUControlWriter handles Modbus writes to vendor Model 64203. Two
// fields are writable: FastPoll (0 normal polling, 1 Service Mode,
// 2 Service Mode while production ≥ FastPollThresholdW) and
// FastPollThresholdW. FastPollKeeper holds both and keeps the ECU flag
// in line (main.exe drops Service Mode after 60 minutes). The setting is
// ECU-wide, so writes are only accepted on the aggregate unit ID.
type ECUControlWriter struct {
	uid      uint8
	keeper   *FastPollKeeper // nil disables writes (no yuneng config dir)
	disabled bool            // writes.allow_fast_polling = false
}

func (w *ECUControlWriter) Apply(ctx context.Context, addrOffset uint16, regs []uint16) error {
	if w.uid != 1 {
		return fmt.Errorf("Model 64203 is ECU-wide; write it on unit ID 1")
	}
	if w.disabled {
		return fmt.Errorf("FastPoll writes disabled (writes.allow_fast_polling=false)")
	}
	if w.keeper == nil {
		return fmt.Errorf("FastPoll writes need the yuneng config dir")
	}
	for off := uint16(0); off < sunspec.ECUControlBodyLen; off++ {
		if writeTouches(addrOffset, regs, off) &&
			off != sunspec.ECUControlBodyFastPollOff && off != sunspec.ECUControlBodyThresholdOff {
			return fmt.Errorf("Model 64203 body offset %d is read-only (FastPoll and FastPollThresholdW are writable)", off)
		}
	}
	if writeTouches(addrOffset, regs, sunspec.ECUControlBodyThresholdOff) {
		if err := w.keeper.SetThresholdW(int(readField(addrOffset, regs, sunspec.ECUControlBodyThresholdOff))); err != nil {
			return err
		}
	}
	if writeTouches(addrOffset, regs, sunspec.ECUControlBodyFastPollOff) {
		return w.keeper.SetMode(int(readField(addrOffset, regs, sunspec.ECUControlBodyFastPollOff)))
	}
	return nil
}
