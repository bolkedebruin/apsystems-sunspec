package sunspec

import "github.com/bolke/ecu-sunspec/internal/source"

// emitNameplate writes SunSpec Model 120 (Nameplate Ratings).
//
// Length is fixed at 26 registers per the SunSpec spec. The values come from
// summing per-inverter NameplateW(); we derive max apparent power and rated
// current from that. PV (DERTyp=4) inverters have unity power factor in
// declaration so VA=W and PFRtg* = 1.0 for all four quadrants.
func emitNameplate(bank *Bank, s source.Snapshot) {
	bank.put16(120, 26)

	totalW := s.TotalNameplateW()
	bank.put16(4) // DERTyp: 4 = PV

	// WRtg / WRtg_SF
	bank.put16(uint16(clampInt32(int32(totalW), 0, 65535)))
	bank.put16(scaleFactor(0))

	// VARtg = WRtg for PV (unity PF rating)
	bank.put16(uint16(clampInt32(int32(totalW), 0, 65535)))
	bank.put16(scaleFactor(0))

	// VArRtgQ1..Q4 (reactive power per quadrant) + scale — not measured
	bank.put16(notImplS16, notImplS16, notImplS16, notImplS16)
	bank.put16(scaleFactor(0))

	// ARtg = WRtg / 230V (rounded), scale -1 (0.1 A)
	var aRtg uint16
	if totalW > 0 {
		aRtg = uint16(float64(totalW)/230.0*10 + 0.5)
	}
	bank.put16(aRtg)
	bank.put16(scaleFactor(-1))

	// PFRtgQ1..Q4 — declared 1.000 (i.e. 1000 with SF=-3) per SunSpec convention
	bank.put16(1000, 1000, 1000, 1000)
	bank.put16(scaleFactor(-3))

	// WHRtg / WHRtg_SF (battery capacity in Wh) — N/A for PV
	bank.put16(notImplU16)
	bank.put16(scaleFactor(0))

	// AhrRtg / AhrRtg_SF (battery Ah) — N/A
	bank.put16(notImplU16)
	bank.put16(scaleFactor(0))

	// MaxChaRte / SF, MaxDisChaRte / SF — N/A for PV
	bank.put16(notImplU16)
	bank.put16(scaleFactor(0))
	bank.put16(notImplU16)
	bank.put16(scaleFactor(0))

	// Pad — Model 120 body length is 26; emitted 25 above so +1.
	bank.put16(0)
}
