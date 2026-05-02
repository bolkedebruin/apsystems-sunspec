package server

import (
	"context"
	"fmt"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// FreqDroopWriter handles Modbus writes to SunSpec Model 711 (DERFreqDroop).
// Exposes the over-frequency droop *curve start* in this iteration:
//
//	body[0]    Ena                 WRITABLE (0/1 — drives Over_frequency_Watt_set enum)
//	body[1]    AdptCtlReq          WRITABLE (request counter, no-op server-side)
//	body[2]    AdptCtlRslt         read-only (server-published async result)
//	body[3]    NCtl                read-only
//	body[4..7] RvrtTms / RvrtRem   read-only
//	body[8]    RvrtCtl             read-only
//	body[9..11] Db/K/RspTms_SF     read-only
//	body[12..13] DbOf  uint32      WRITABLE — OF deadband from 50 Hz (cHz)
//	body[14..15] DbUf  uint32      read-only (UF-side not yet mapped)
//	body[16]   KOf                 read-only THIS ITERATION — APsystems DD code
//	                               doesn't appear to be %P/Hz slope; mapping
//	                               needs ground-truth empirical work before
//	                               we expose it as writable.
//	body[17]   KUf                 read-only
//	body[18..19] RspTms uint32     read-only
//	body[20]   PMin                read-only
//	body[21]   ReadOnly            read-only
//
// Long-name dispatch via Writer.SetProtectionParam:
//
//	Ena   → Over_frequency_Watt_set (CV: 13 AS/NZS, 14 other, 15 disabled)
//	DbOf  → Over_frequency_Watt_Start_set (Hz = 50.0 + DbOf/100)
//
// Cross-parameter check: the deadband-derived HzStart must sit at least
// 0.5 Hz below the inverter's currently loaded OF1 trip (AK / OFFast).
// This guarantees curtailment begins before the trip threshold.
//
// We validate against the INVERTER's own snapshot.Protection[uid].OFFast —
// firmware truth, not a host-side cache.
type FreqDroopWriter struct {
	uid    uint8
	snap   source.Snapshot
	writer *source.Writer
}

const (
	// Body offsets within Model 711 (excluding ID + L).
	freqDroopBodyEna       = 0
	freqDroopBodyAdptReq   = 1
	freqDroopBodyAdptRslt  = 2
	freqDroopBodyDbOfHi    = 12
	freqDroopBodyDbOfLo    = 13
	freqDroopBodyKOf       = 16

	// OF-trip safety margin (Hz). HzStart (where curtailment begins) must
	// sit at least this far below the inverter's OF1 trip — guarantees the
	// curve has frequency room to actuate before trip-disconnect.
	freqDroopOFMarginHz = 0.5

	// Mode enum values for Over_frequency_Watt_set.
	freqDroopModeASNZS    = 13
	freqDroopModeOther    = 14
	freqDroopModeDisabled = 15
)

func (fdw *FreqDroopWriter) Apply(ctx context.Context, addrOffset uint16, regs []uint16) error {
	if fdw == nil || fdw.writer == nil {
		return fmt.Errorf("writes disabled or writer not configured")
	}
	if fdw.uid <= 1 {
		return fmt.Errorf("Model 711 writes only valid on per-inverter unit IDs (2..N+1)")
	}
	idx := int(fdw.uid) - 2
	if idx < 0 || idx >= len(fdw.snap.Inverters) {
		return fmt.Errorf("unit ID %d not mapped to any inverter", fdw.uid)
	}
	uid := fdw.snap.Inverters[idx].UID
	prot := fdw.snap.Protection[uid]

	// Determine which fields the write touches. Only Ena, AdptCtlReq, DbOf
	// (both halves) are writable in this iteration; KOf and others stay
	// read-only until the APsystems DD-code semantic is ground-truthed.
	wantsEna := writeTouches(addrOffset, regs, freqDroopBodyEna)
	wantsReq := writeTouches(addrOffset, regs, freqDroopBodyAdptReq)
	wantsDbOf := writeTouches(addrOffset, regs, freqDroopBodyDbOfHi) &&
		writeTouches(addrOffset, regs, freqDroopBodyDbOfLo)

	// Reject writes that touch any other register in the model body.
	for off := uint16(0); off < 22; off++ {
		if !writeTouches(addrOffset, regs, off) {
			continue
		}
		switch off {
		case freqDroopBodyEna, freqDroopBodyAdptReq,
			freqDroopBodyDbOfHi, freqDroopBodyDbOfLo:
			// writable, fine
		case freqDroopBodyAdptRslt:
			return fmt.Errorf("AdptCtlRslt is server-published (read-only)")
		default:
			return fmt.Errorf("Model 711 body offset %d is read-only this iteration "+
				"(only Ena, AdptCtlReq, DbOf are writable)", off)
		}
	}
	if !(wantsEna || wantsDbOf) {
		if wantsReq {
			return nil // no-op counter bump
		}
		return fmt.Errorf("Model 711 write must touch at least one writable field (Ena/DbOf)")
	}

	// Cross-parameter sanity using the inverter's currently-loaded OF1.
	if !prot.Has["AK"] {
		return fmt.Errorf("cannot validate against inverter's OF1 trip (AK not yet read from %s)", uid)
	}
	if wantsDbOf {
		hi := readField(addrOffset, regs, freqDroopBodyDbOfHi)
		lo := readField(addrOffset, regs, freqDroopBodyDbOfLo)
		dbOf := uint32(hi)<<16 | uint32(lo)
		hzStart := 50.0 + float64(dbOf)/100.0
		if hzStart <= 50.0 {
			return fmt.Errorf("HzStart %.2f Hz must be > 50 Hz (above nominal)", hzStart)
		}
		if hzStart > prot.OFFast-freqDroopOFMarginHz {
			return fmt.Errorf("HzStart %.2f Hz must sit ≥ %.1f Hz below OF1 trip %.2f Hz (active on %s)",
				hzStart, freqDroopOFMarginHz, prot.OFFast, uid)
		}
		if err := fdw.writer.SetProtectionParam(ctx, uid, "Over_frequency_Watt_Start_set", hzStart); err != nil {
			return fmt.Errorf("set Over_frequency_Watt_Start_set: %w", err)
		}
	}
	if wantsEna {
		ena := readField(addrOffset, regs, freqDroopBodyEna)
		// Map SunSpec Ena (0/1) to APsystems' enum (13 = AS/NZS, 14 = other, 15 = disabled).
		modeVal := freqDroopModeOther
		if ena == 0 {
			modeVal = freqDroopModeDisabled
		} else if int(prot.OFDroopMode) == freqDroopModeASNZS {
			modeVal = freqDroopModeASNZS // preserve current AS/NZS mode
		}
		if err := fdw.writer.SetProtectionParam(ctx, uid, "Over_frequency_Watt_set", float64(modeVal)); err != nil {
			return fmt.Errorf("set Over_frequency_Watt_set: %w", err)
		}
	}
	return nil
}
