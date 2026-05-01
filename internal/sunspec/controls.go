package sunspec

import "github.com/bolke/ecu-sunspec/internal/source"

// SunSpec Inverter Controls model. Length 24 registers (body, excluding ID + L).
const (
	ControlsModelID uint16 = 123
	ControlsBodyLen uint16 = 24
)

// Field offsets within the Model 123 body (0 = first reg after the L
// register). Used by the writer code to map register addresses to actions.
//
// Layout per SunSpec:
//
//	+0  Conn_WinTms        uint16   sec     window for Conn
//	+1  Conn_RvrtTms       uint16   sec     auto-revert
//	+2  Conn               enum16   0=disconnect, 1=connect
//	+3  WMaxLim_Pct        uint16   %       cap as % of nameplate
//	+4  WMaxLim_Pct_WinTms uint16   sec
//	+5  WMaxLim_Pct_RvrtTms uint16  sec
//	+6  WMaxLim_Pct_RmpTms uint16   sec
//	+7  WMaxLim_Ena        enum16   0=off, 1=on
//	+8  OutPFSet           int16    PF*1000  not implemented for APsystems
//	+9  OutPFSet_WinTms    uint16
//	+10 OutPFSet_RvrtTms   uint16
//	+11 OutPFSet_RmpTms    uint16
//	+12 OutPFSet_Ena       enum16
//	+13 VArWMaxPct         int16    not implemented
//	+14 VArMax_WinTms      uint16
//	+15 VArMax_RvrtTms     uint16
//	+16 VArMax_RmpTms      uint16
//	+17 VArPct_Mod         enum16
//	+18 VArPct_Ena         enum16
//	+19 WMaxLim_Pct_SF     sunssf
//	+20 OutPFSet_SF        sunssf
//	+21 VArPct_SF          sunssf
//	+22 (pad)
//	+23 (pad)
const (
	OffControlsConnWinTms        uint16 = 0
	OffControlsConnRvrtTms       uint16 = 1
	OffControlsConn              uint16 = 2
	OffControlsWMaxLimPct        uint16 = 3
	OffControlsWMaxLimPctWinTms  uint16 = 4
	OffControlsWMaxLimPctRvrtTms uint16 = 5
	OffControlsWMaxLimPctRmpTms  uint16 = 6
	OffControlsWMaxLimEna        uint16 = 7
	OffControlsOutPFSet          uint16 = 8
	OffControlsWMaxLimPctSF      uint16 = 19
)

// emitControls writes a Model 123 (Inverter Controls) section. Values reflect
// the current state read from the snapshot — what ALL inverters average to
// for the aggregate bank, or this one inverter's state for a per-inverter
// bank.
//
// The model is emit-only here; clients accept writes via the server's
// Modbus-write handler, which dispatches them through internal/source.Writer.
func emitControls(bank *Bank, currentPct uint16, ena uint16, conn uint16) {
	bank.put16(ControlsModelID, ControlsBodyLen)

	bank.put16(0)          // Conn_WinTms — not used (immediate)
	bank.put16(0)          // Conn_RvrtTms
	bank.put16(conn)       // Conn — 1=connected, 0=disconnected
	bank.put16(currentPct) // WMaxLim_Pct — current cap %
	bank.put16(0)          // WMaxLim_Pct_WinTms
	bank.put16(0)          // WMaxLim_Pct_RvrtTms — auto-revert not supported
	bank.put16(0)          // WMaxLim_Pct_RmpTms
	bank.put16(ena)        // WMaxLim_Ena

	bank.put16(notImplS16) // OutPFSet
	bank.put16(0)          // OutPFSet_WinTms
	bank.put16(0)          // OutPFSet_RvrtTms
	bank.put16(0)          // OutPFSet_RmpTms
	bank.put16(0)          // OutPFSet_Ena (disabled)

	bank.put16(notImplS16) // VArWMaxPct
	bank.put16(0)          // VArMax_WinTms
	bank.put16(0)          // VArMax_RvrtTms
	bank.put16(0)          // VArMax_RmpTms
	bank.put16(0)          // VArPct_Mod
	bank.put16(0)          // VArPct_Ena

	bank.put16(scaleFactor(0)) // WMaxLim_Pct_SF — % is integer
	bank.put16(scaleFactor(0)) // OutPFSet_SF
	bank.put16(scaleFactor(0)) // VArPct_SF

	bank.put16(0, 0) // padding to length 24
}

// AggregateControlsState computes the current Model 123 values for the
// aggregate bank (uid 1) from the snapshot.
//
// WMaxLim_Pct: average across inverters of (limitedpower / max) × 100.
// WMaxLim_Ena: 1 if any inverter has limitedpower < max, else 0.
// Conn: 1 if any inverter online, 0 if all offline.
func AggregateControlsState(s source.Snapshot) (pct uint16, ena uint16, conn uint16) {
	if len(s.Inverters) == 0 {
		return 100, 0, 0
	}
	totalPct := 0
	limited := 0
	online := 0
	for _, inv := range s.Inverters {
		// Per-panel cap → % of MaxPanelLimitW.
		cap := inv.LimitedPowerW
		if cap == 0 {
			cap = source.MaxPanelLimitW
		}
		thisPct := (cap * 100) / source.MaxPanelLimitW
		if thisPct > 100 {
			thisPct = 100
		}
		totalPct += thisPct
		if cap < source.MaxPanelLimitW {
			limited++
		}
		if inv.Online {
			online++
		}
	}
	avgPct := totalPct / len(s.Inverters)
	pct = uint16(avgPct)
	if limited > 0 {
		ena = 1
	}
	if online > 0 {
		conn = 1
	}
	return
}

// PerInverterControlsState computes Model 123 values for one inverter
// (uid 2..N+1).
func PerInverterControlsState(inv source.Inverter) (pct uint16, ena uint16, conn uint16) {
	cap := inv.LimitedPowerW
	if cap == 0 {
		cap = source.MaxPanelLimitW
	}
	p := (cap * 100) / source.MaxPanelLimitW
	if p > 100 {
		p = 100
	}
	pct = uint16(p)
	if cap < source.MaxPanelLimitW {
		ena = 1
	}
	if inv.Online {
		conn = 1
	}
	return
}
