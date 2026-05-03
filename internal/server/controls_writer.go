package server

import (
	"context"
	"fmt"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
)

// ControlsWriter applies Modbus writes that target SunSpec Model 123
// (Inverter Controls). It translates the register-level operation into the
// SQL-table mutations main.exe expects (writer.SetMaxPower /
// writer.SetTurnOnOff).
//
// uid identifies which bank the write came in on:
//
//	uid 1     → applies to ALL inverters in snap.Inverters (aggregate)
//	uid 2..N+1 → applies to snap.Inverters[uid-2] only
type ControlsWriter struct {
	uid      uint8
	snap     source.Snapshot
	writer   *source.Writer
	reverter *Reverter
}

// Apply takes a slice of registers freshly written by a client (offsets
// relative to the Model 123 body, length up to ControlsBodyLen) and dispatches
// the resulting SQL operations.
//
// The write semantics mirror the standard SunSpec definitions:
//
//	WMaxLim_Pct + WMaxLim_Ena=1 → cap each affected inverter's per-panel
//	                              watts to (MaxPanelLimitW × pct / 100).
//	WMaxLim_Ena=0               → restore to MaxPanelLimitW (full output).
//	WMaxLim_Pct_RvrtTms > 0     → schedule auto-revert in N seconds. If the
//	                              controller fails to refresh within that
//	                              window, the cap is lifted (pre-2018
//	                              Model 123 reversion semantics).
//	Conn=0                      → turn the inverter(s) off.
//	Conn=1                      → turn the inverter(s) back on.
func (cw *ControlsWriter) Apply(ctx context.Context, addrOffset uint16, regs []uint16) error {
	if cw == nil || cw.writer == nil {
		return fmt.Errorf("writes disabled or writer not configured")
	}

	targets := cw.targetUIDs()
	if len(targets) == 0 {
		return fmt.Errorf("no inverters mapped to unit ID %d", cw.uid)
	}

	enaWritten := writeTouches(addrOffset, regs, sunspec.OffControlsWMaxLimEna)
	pctWritten := writeTouches(addrOffset, regs, sunspec.OffControlsWMaxLimPct)
	rvrtWritten := writeTouches(addrOffset, regs, sunspec.OffControlsWMaxLimPctRvrtTms)

	// WMaxLim_Ena=0 written explicitly → restore full output and cancel any
	// pending reversion. WMaxLimPct written (with or without Ena) → apply
	// that cap; if RvrtTms is non-zero, arm the reverter. Ena=1 written
	// without Pct → no DB action (existing cap stays).
	if enaWritten && readField(addrOffset, regs, sunspec.OffControlsWMaxLimEna) == 0 {
		cw.reverter.Cancel(cw.uid)
		if err := cw.restoreFull(ctx, targets); err != nil {
			return err
		}
	} else if pctWritten {
		pct := readField(addrOffset, regs, sunspec.OffControlsWMaxLimPct)
		if err := cw.applyCap(ctx, targets, pct); err != nil {
			return err
		}
		if rvrtWritten {
			rvrt := readField(addrOffset, regs, sunspec.OffControlsWMaxLimPctRvrtTms)
			cw.armOrCancelReversion(rvrt, targets)
		}
	}

	// Did the write touch the Conn (connect/disconnect) field? Apply on/off.
	if writeTouches(addrOffset, regs, sunspec.OffControlsConn) {
		conn := readField(addrOffset, regs, sunspec.OffControlsConn)
		if err := cw.applyConn(ctx, targets, conn); err != nil {
			return err
		}
	}

	return nil
}

// targetUIDs returns the inverter UIDs the write should affect.
func (cw *ControlsWriter) targetUIDs() []string {
	if cw.uid <= 1 {
		// aggregate
		out := make([]string, 0, len(cw.snap.Inverters))
		for _, inv := range cw.snap.Inverters {
			out = append(out, inv.UID)
		}
		return out
	}
	idx := int(cw.uid) - 2
	if idx < 0 || idx >= len(cw.snap.Inverters) {
		return nil
	}
	return []string{cw.snap.Inverters[idx].UID}
}

func (cw *ControlsWriter) restoreFull(ctx context.Context, uids []string) error {
	for _, uid := range uids {
		if err := cw.writer.RestoreFullPower(ctx, uid); err != nil {
			return fmt.Errorf("restore %s: %w", uid, err)
		}
	}
	return nil
}

func (cw *ControlsWriter) applyCap(ctx context.Context, uids []string, pct uint16) error {
	if pct > 100 {
		pct = 100
	}
	watts := source.MaxPanelLimitW * int(pct) / 100
	if watts < source.MinPanelLimitW {
		watts = source.MinPanelLimitW
	}
	for _, uid := range uids {
		if err := cw.writer.SetMaxPower(ctx, uid, watts); err != nil {
			return fmt.Errorf("setmax %s: %w", uid, err)
		}
	}
	return nil
}

// armOrCancelReversion translates the wire-format RvrtTms value into a
// reverter Schedule call. The SunSpec uint16 not-implemented sentinel
// (0xFFFF) and zero are both treated as "no auto-revert."
func (cw *ControlsWriter) armOrCancelReversion(rvrtTmsSec uint16, targets []string) {
	if rvrtTmsSec == 0 || rvrtTmsSec == 0xFFFF {
		cw.reverter.Cancel(cw.uid)
		return
	}
	cw.reverter.Schedule(cw.uid, time.Duration(rvrtTmsSec)*time.Second, targets)
}

func (cw *ControlsWriter) applyConn(ctx context.Context, uids []string, conn uint16) error {
	on := conn != 0
	for _, uid := range uids {
		if err := cw.writer.SetTurnOnOff(ctx, uid, on); err != nil {
			return fmt.Errorf("turn %s: %w", uid, err)
		}
	}
	return nil
}

// writeTouches reports whether the FC16 write at addrOffset (relative to the
// Model 123 body) covers a particular field offset.
func writeTouches(addrOffset uint16, regs []uint16, fieldOff uint16) bool {
	if fieldOff < addrOffset {
		return false
	}
	if uint16(len(regs)) <= fieldOff-addrOffset {
		return false
	}
	return true
}

// readField extracts a field value from a partial write. Returns 0 if the
// write didn't include this field.
func readField(addrOffset uint16, regs []uint16, fieldOff uint16) uint16 {
	if !writeTouches(addrOffset, regs, fieldOff) {
		return 0
	}
	return regs[fieldOff-addrOffset]
}
