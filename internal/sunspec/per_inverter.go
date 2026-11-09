package sunspec

import (
	"strconv"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// EncodePerInverter produces a self-contained SunSpec bank for a single
// microinverter. Layout is the SunSpec equivalent of "one Fronius behind the
// Datalogger" — Common Model identifies the microinverter, the Inverter Model
// (101 single-phase, 103 for the three-phase YC1000 / QT2) exposes its electrical
// state, and Multi-MPPT (160) lists
// its panels.
//
// The bank is intended to be served at a per-inverter Modbus unit ID, so a
// SunSpec scanner walking unit IDs 2..N+1 sees N microinverters as separate
// devices.
func EncodePerInverter(inv source.Inverter, ecuid string, unitID uint16, opt Options) Bank {
	return EncodePerInverterWithProtection(inv, ecuid, unitID, opt, source.ProtectionParams{Has: map[string]bool{}})
}

// EncodePerInverterWithProtection is like EncodePerInverter but also surfaces
// IEEE 1547-2018 DER trip + Enter Service models populated from the supplied
// protection params for this inverter.
func EncodePerInverterWithProtection(inv source.Inverter, ecuid string, unitID uint16, opt Options, prot source.ProtectionParams) Bank {
	if opt.Manufacturer == "" {
		opt.Manufacturer = DefaultManufacturer
	}

	bank := Bank{Regs: make([]uint16, 0, 256), Base: BaseRegister}

	bank.put16(0x5375, 0x6E53) // SunS

	// --- Common Model ---
	bank.put16(CommonModelID, CommonModelBodyLen)
	bank.putString(opt.Manufacturer, 16)
	bank.putString(inverterModelLabel(inv), 16)
	bank.putString("", 8)                            // Opt
	bank.putString(strconv.Itoa(inv.SoftwareVer), 8) // Vr
	bank.putString(inv.UID, 16)                      // SN = inverter UID
	bank.put16(unitID)                               // DA — Modbus unit address
	bank.put16(0)                                    // pad

	// --- Inverter Model 101 (single-phase) or 103 (three-phase YC1000) ---
	phases := 1
	modelID := InverterModelSinglePhase
	if inv.ThreePhase() {
		phases = 3
		modelID = InverterModelThreePhase
	}
	bank.put16(modelID, InverterModelBodyLen)

	// A / per-phase A — derive from W/V; 0.1 A units (ASF=-1). Three-phase
	// output is balanced, so each phase carries a third of the total.
	var a uint16
	if inv.ACVoltageV > 0 && inv.ACPowerW >= 0 {
		amps10 := float64(inv.ACPowerW) / float64(inv.ACVoltageV) * 10
		if amps10 < 65535 {
			a = uint16(amps10 + 0.5)
		}
	}
	aph := a
	if phases == 3 && inv.ACVoltageV > 0 && inv.ACPowerW >= 0 {
		aph = uint16(float64(inv.ACPowerW)/3/float64(inv.ACVoltageV)*10 + 0.5)
	}
	bank.put16(a)
	bank.put16(aph, phaseValueU16(aph, 1, phases), phaseValueU16(aph, 2, phases)) // AphA, AphB, AphC
	bank.put16(scaleFactor(-1))                                                   // ASF

	// PPVphAB/BC/CA — line-line voltages, measured by three-phase inverters only
	if ll := inv.LineToLineV(); ll[0] > 0 {
		bank.put16(uint16(ll[0]), uint16(ll[1]), uint16(ll[2]))
	} else {
		bank.put16(notImplU16, notImplU16, notImplU16)
	}

	// PhVphA/B/C — line-neutral, unused phases unimplemented
	v := uint16(0)
	if inv.ACVoltageV > 0 && inv.ACVoltageV < 65535 {
		v = uint16(inv.ACVoltageV)
	}
	bank.put16(v, phaseValueU16(v, 1, phases), phaseValueU16(v, 2, phases))
	bank.put16(scaleFactor(0)) // VSF

	// W / WSF
	w := int32(inv.ACPowerW)
	bank.put16(uint16(int16(clampInt32(w, -32760, 32760))))
	bank.put16(scaleFactor(0))

	// Hz / HzSF — ×100
	hz := uint16(0)
	if inv.FrequencyHz > 0 {
		hz = uint16(inv.FrequencyHz*100 + 0.5)
	}
	bank.put16(hz)
	bank.put16(scaleFactor(-2))

	// VA / VAr / PF — emitted as "not implemented" by design.
	//
	// APsystems' own SunSpec server (port 502 on the ECU) reports values
	// here for DS3-family inverters by computing VA = A_measured × V,
	// PF = W/VA, VAr = √(VA² − W²). For QS1 it derives A from W/V, so
	// VA collapses to W and Q is structurally zero — VAr=0, PF=1.0
	// regardless of actual conditions.
	//
	// We don't capture the inverters' independently measured A in our
	// snapshot today (parameters_app.conf surfaces W and V but not the
	// per-channel measured A on DS3), so geometric Q can't be honestly
	// computed without proxying APsystems' :502. We choose to emit
	// not-impl rather than fake VAr=0/PF=1 stand-ins for inverters that
	// don't measure Q.
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))

	// WH (acc32) — per-inverter lifetime not tracked; emit 0 (acc32 sentinel)
	bank.putAcc32(0)
	bank.put16(scaleFactor(0))

	// DC fields not exposed per-inverter
	bank.put16(notImplU16) // DCA
	bank.put16(scaleFactor(0))
	bank.put16(notImplU16) // DCV
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16) // DCW
	bank.put16(scaleFactor(0))

	// Tmp
	bank.put16(uint16(int16(clampInt32(int32(inv.TemperatureC), -100, 200))))
	bank.put16(notImplS16) // TmpSnk
	bank.put16(notImplS16) // TmpTrns
	bank.put16(notImplS16) // TmpOt
	bank.put16(scaleFactor(0))

	// St
	bank.put16(inverterOperatingState(inv.Online, inv.ACPowerW))
	bank.put16(0) // StVnd

	// Evt1 = SunSpec-standard event flags translated from this inverter's
	// 86-bit APsystems bitstring. EvtVnd1..3 carry the raw bits for
	// consumers that want the unmapped per-channel/per-stage detail.
	bank.putAcc32(uint64(MapAPsystemsToSunSpecEvt1(inv.EventBits))) // Evt1
	bank.putAcc32(uint64(notImplU32))                               // Evt2
	bank.putAcc32(uint64(inv.EventBits[0]))                         // EvtVnd1 — raw 0-31
	bank.putAcc32(uint64(inv.EventBits[1]))                         // EvtVnd2 — raw 32-63
	bank.putAcc32(uint64(inv.EventBits[2]))                         // EvtVnd3 — raw 64-95
	bank.putAcc32(uint64(notImplU32))                               // EvtVnd4 — unused

	// --- Inverter Float Model 111/113 — same field set as 101/103, float32 encoding ---
	emitInverterFloatPerInverter(&bank, inv)

	// --- Model 114 — DC Data float (per-panel) ---
	emitDCDataFloatPerInverter(&bank, inv)

	// --- Model 120 — Nameplate Ratings for THIS inverter ---
	// Per-inverter WRtg comes from inv.NameplateW() — the model_int → watts
	// table (loaded from sunspec-nameplate.json) when this submodel is
	// catalogued, else a TypeCode-based fallback.
	emitNameplatePerInverter(&bank, inv)

	// --- Model 123 — Inverter Controls (read/write) for THIS inverter ---
	pct, ena, conn := PerInverterControlsState(inv)
	emitControls(&bank, pct, ena, conn)

	// --- Models 707/708/709/710 + 703 + 711 — DER trip + Enter Service + Freq Droop ---
	// Sourced from this inverter's active protection_parameters60code row.
	// vNom: prefer this inverter's reported AC voltage, fall back to 230 V.
	vNom := float64(inv.ACVoltageV)
	emitDERTripModels(&bank, prot, vNom)
	emitEnterService(&bank, prot, vNom)
	{
		rslt, req := lookupWriteRslt(opt, uint8(unitID), FreqDroopModelID)
		emitFreqDroop(&bank, prot, rslt, req)
	}
	emitFreqWattCurve(&bank, prot)

	// --- Multi-MPPT (160) — only this inverter's panels ---
	if !opt.DisableMPPT {
		emitPerInverterMPPT(&bank, inv)
	}

	bank.put16(EndModelID, 0)
	return bank
}

// emitPerInverterMPPT appends Model 160 with this inverter's panels; same
// encoding as the aggregate (see emitMultiMPPT), offline panels included.
func emitPerInverterMPPT(bank *Bank, inv source.Inverter) {
	emitMPPT(bank, mpptPanels([]source.Inverter{inv}))
}

// isQT2Model reports whether id.model belongs to the QT2 family — the same
// codes internal/server/profiles.go routes to the QT2 frame builder.
func isQT2Model(model int) bool {
	switch model {
	case 0x29, 0x30, 0x31, 0x32:
		return true
	}
	return false
}

// inverterModelLabel returns the Common Model `Md` for a per-inverter bank.
//
// The mapping comes from APsystems' wire-protocol type code (parameters_app.conf
// column 2). The same code list is used by HA's homeassistant-apsystems_ecu_reader:
//
//	"01" — YC600 / DS3        (2-channel)
//	"03" — QS1                (4-channel)
//	"04" — DS3-H / DS3D-L     (2-channel)
//	"05" — QT2                (4-channel three-phase)
//	"02" — YC1000 / QT2       (4-channel three-phase)
//
// ECU-C fw C1.2.27A reports a QT2 as type "02" (id.model 41 = 0x29), so
// the QT2 family's id.model codes tell it apart from the YC1000.
//
// The vendor name lives in `Mn`, so don't repeat it here — Venus and HA
// combine Mn + Md when displaying.
func inverterModelLabel(inv source.Inverter) string {
	switch inv.TypeCode {
	case "01":
		return "DS3"
	case "02":
		if isQT2Model(inv.Model) {
			return "QT2"
		}
		return "YC1000"
	case "03":
		return "QS1"
	case "04":
		return "DS3-H"
	case "05":
		return "QT2"
	}
	if inv.TypeCode != "" {
		return "type-" + inv.TypeCode
	}
	return "microinverter"
}
